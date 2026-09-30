// Package app is the gateway's use case: IngestService runs the pipeline for one inbound message
// (MQTT message or HTTPS batch) and hands every output to the Producer in a single call.
// It depends only on ports; concrete Kafka/Redis/proto code lives in adapters.
package app

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/canonical"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/dedup"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/pipeline"
)

const (
	TopicCanonical = "telemetry.canonical.v1"
	TopicDLQ       = "telemetry.dlq.v1"
	MaxBody        = 256 << 10
	MaxBatch       = 500
	dlqMaxValue    = 64 << 10 // oversize/auth payloads are truncated in the DLQ
)

func RawTopic(oem string) string { return "oem.raw." + oem + ".v1" }

// ---- ports ----

type Decoder interface {
	Decode(body []byte, batch bool) ([]canonical.Record, *canonical.Reject)
}

type Header struct {
	Key   string
	Value string
}

// Out is one Kafka record to produce.
type Out struct {
	Topic   string
	Key     string
	Value   []byte
	Headers []Header
}

// Producer writes all outs and returns only when every one is acknowledged (acks=all), or an error.
type Producer interface {
	Produce(ctx context.Context, outs []Out) error
}

// Encoder serialises a canonical event (protobuf in production).
type Encoder interface {
	Marshal(e *canonical.Event) ([]byte, error)
}

// Confirmer is the exact dedup store (Redis).
type Confirmer interface {
	// ConfirmNew atomically records keys and reports, per key, whether it was new.
	ConfirmNew(ctx context.Context, keys []string) ([]bool, error)
	// Remember records keys asynchronously (first sightings); must not block.
	Remember(keys []string)
}

// Observer receives metrics events.
type Observer interface {
	Accepted(oem string, n int)
	Rejected(oem string, reason canonical.Reason, n int)
	Duplicates(n int)
	DedupDegraded()
	ProduceLatency(d time.Duration)
}

// ---- service ----

type Message struct {
	OEM      string
	TopicVIN string // MQTT only
	Body     []byte
	Batch    bool
}

type Result struct {
	Accepted, Duplicates int
	Rejected             map[canonical.Reason]int
}

type Service struct {
	Decoders  map[string]Decoder
	Chain     pipeline.Chain
	Encoder   Encoder
	Producer  Producer
	Confirmer Confirmer // nil → dedup degraded (Bloom-only, never drops)
	Observer  Observer
	Now       func() time.Time

	mu    sync.Mutex
	bloom *dedup.Rotating
}

// NewService completes s (Bloom pair sized for eps events/s over a 10-minute window) and returns it.
func NewService(eps int, s *Service) *Service {
	if s.Now == nil {
		s.Now = time.Now
	}
	s.bloom = dedup.NewRotating(eps*300, 0.01, 600_000, s.Now().UnixMilli())
	return s
}

func dlq(key string, value []byte, oem string, r *canonical.Reject, receivedMs int64) Out {
	if len(value) > dlqMaxValue {
		value = value[:dlqMaxValue]
	}
	if key == "" {
		key = "unknown"
	}
	return Out{Topic: TopicDLQ, Key: key, Value: value, Headers: []Header{
		{"x-dlq-reason", string(r.Reason)}, {"x-oem", oem}, {"x-error", r.Detail}, {"x-received-ms", strconv.FormatInt(receivedMs, 10)},
	}}
}

// Reject sends a whole message to the DLQ (used by transports for AUTH / IDENTITY / OVERSIZE).
func (s *Service) Reject(ctx context.Context, m Message, r *canonical.Reject) error {
	s.Observer.Rejected(m.OEM, r.Reason, 1)
	return s.produce(ctx, []Out{dlq(m.TopicVIN, m.Body, m.OEM, r, s.Now().UnixMilli())})
}

func (s *Service) produce(ctx context.Context, outs []Out) error {
	start := time.Now()
	err := s.Producer.Produce(ctx, outs)
	s.Observer.ProduceLatency(time.Since(start))
	return err
}

// Handle runs pipeline steps 2–12 (step 1, authentication, belongs to the transport). It returns an
// error only if Kafka did not acknowledge: the transport must then not ack (MQTT) / return 503.
func (s *Service) Handle(ctx context.Context, m Message) (Result, error) {
	res := Result{Rejected: map[canonical.Reason]int{}}
	nowMs := s.Now().UnixMilli()
	var outs []Out
	rejectAll := func(r *canonical.Reject) (Result, error) {
		res.Rejected[r.Reason]++
		s.Observer.Rejected(m.OEM, r.Reason, 1)
		return res, s.produce(ctx, []Out{dlq(m.TopicVIN, m.Body, m.OEM, r, nowMs)})
	}

	// 2. size
	if len(m.Body) > MaxBody {
		return rejectAll(canonical.Rejectf(canonical.Oversize, "body %d bytes > %d", len(m.Body), MaxBody))
	}
	// 4. adapter selection (before 3: the raw topic is per OEM, so an unknown OEM has none)
	dec, ok := s.Decoders[m.OEM]
	if !ok {
		return rejectAll(canonical.Rejectf(canonical.UnknownOEM, "oem %q", m.OEM))
	}
	// 3. publish raw (replayable after an adapter fix)
	outs = append(outs, Out{Topic: RawTopic(m.OEM), Key: m.TopicVIN, Value: m.Body,
		Headers: []Header{{"x-received-ms", strconv.FormatInt(nowMs, 10)}}})
	// 5. decode
	recs, rej := dec.Decode(m.Body, m.Batch)
	if rej == nil && len(recs) > MaxBatch {
		rej = canonical.Rejectf(canonical.Oversize, "%d records > %d", len(recs), MaxBatch)
	}
	if rej != nil {
		res.Rejected[rej.Reason]++
		s.Observer.Rejected(m.OEM, rej.Reason, 1)
		outs = append(outs, dlq(m.TopicVIN, m.Body, m.OEM, rej, nowMs))
		return res, s.produce(ctx, outs)
	}

	// 6–9. validation chain
	meta := &pipeline.Meta{OEM: m.OEM, TopicVIN: m.TopicVIN, ReceivedMs: nowMs}
	valid := recs[:0]
	for _, r := range recs {
		if r.Reject == nil {
			r.Reject = s.Chain.Run(meta, &r.Event)
		}
		if r.Reject != nil {
			res.Rejected[r.Reject.Reason]++
			s.Observer.Rejected(m.OEM, r.Reject.Reason, 1)
			key := r.Event.VIN
			if key == "" {
				key = m.TopicVIN
			}
			outs = append(outs, dlq(key, r.Raw, m.OEM, r.Reject, nowMs))
			continue
		}
		valid = append(valid, r)
	}

	// 10. dedup
	valid = s.dedup(ctx, valid, nowMs, &res)

	// 11–12. enrich + publish canonical
	for i := range valid {
		e := &valid[i].Event
		e.EventID, e.TsIngestMs = UUIDv7(nowMs), nowMs
		b, err := s.Encoder.Marshal(e)
		if err != nil {
			return res, fmt.Errorf("encode canonical: %w", err)
		}
		outs = append(outs, Out{Topic: TopicCanonical, Key: e.VIN, Value: b})
	}
	res.Accepted = len(valid)
	if err := s.produce(ctx, outs); err != nil {
		return res, err
	}
	s.Observer.Accepted(m.OEM, res.Accepted)
	return res, nil
}

func dedupKey(vin string, seq uint64) string {
	return "dedup:" + vin + ":" + strconv.FormatUint(seq, 10)
}

// dedup drops records the Bloom filter has maybe seen AND Redis confirms were seen. Bloom misses
// are kept and remembered in Redis asynchronously. If Redis fails, everything is kept (degraded).
func (s *Service) dedup(ctx context.Context, recs []canonical.Record, nowMs int64, res *Result) []canonical.Record {
	var hitIdx []int
	var newKeys []string
	s.mu.Lock()
	for i := range recs {
		e := &recs[i].Event
		if s.bloom.SeenOrAdd(e.VIN, e.Seq, nowMs) {
			hitIdx = append(hitIdx, i)
		} else {
			newKeys = append(newKeys, dedupKey(e.VIN, e.Seq))
		}
	}
	// Remember under the Bloom lock: a concurrent duplicate that hits the Bloom filter must find the
	// key at least in the confirmer's pending set.
	if s.Confirmer != nil && len(newKeys) > 0 {
		s.Confirmer.Remember(newKeys)
	}
	s.mu.Unlock()
	if s.Confirmer == nil {
		if len(hitIdx) > 0 {
			s.Observer.DedupDegraded()
		}
		return recs
	}
	if len(hitIdx) == 0 {
		return recs
	}
	keys := make([]string, len(hitIdx))
	for j, i := range hitIdx {
		keys[j] = dedupKey(recs[i].Event.VIN, recs[i].Event.Seq)
	}
	isNew, err := s.Confirmer.ConfirmNew(ctx, keys)
	if err != nil || len(isNew) != len(keys) {
		s.Observer.DedupDegraded() // Redis down: keep everything, sinks are idempotent
		return recs
	}
	drop := map[int]bool{}
	for j, i := range hitIdx {
		if !isNew[j] {
			drop[i] = true
		}
	}
	if len(drop) == 0 {
		return recs
	}
	kept := recs[:0]
	for i, r := range recs {
		if !drop[i] {
			kept = append(kept, r)
		}
	}
	res.Duplicates = len(drop)
	s.Observer.Duplicates(len(drop))
	return kept
}

// UUIDv7 returns an RFC 9562 version-7 UUID: 48-bit Unix ms timestamp, then random bits.
func UUIDv7(ms int64) string {
	var b [16]byte
	_, _ = rand.Read(b[6:])
	binary.BigEndian.PutUint64(b[:8], uint64(ms)<<16|uint64(b[6])<<8|uint64(b[7]))
	b[6] = b[6]&0x0F | 0x70 // version 7
	b[8] = b[8]&0x3F | 0x80 // RFC 4122 variant
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
