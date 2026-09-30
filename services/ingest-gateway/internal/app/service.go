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

// Confirmer is the exact dedup store (Redis). Seen is read-only; keys are recorded with Remember
// only after the records were acknowledged by Kafka, so a failed produce leaves no dedup state
// behind and the sender's retry is never mistaken for a duplicate (that would be silent loss).
type Confirmer interface {
	// Seen reports, per key, whether it was already recorded.
	Seen(ctx context.Context, keys []string) ([]bool, error)
	// Remember records keys asynchronously; must not block.
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

	mu       sync.Mutex
	bloom    *dedup.Rotating
	inflight map[string]int // keys of records whose produce is in progress
}

// NewService completes s (Bloom pair sized for eps events/s over a 10-minute window) and returns it.
func NewService(eps int, s *Service) *Service {
	if s.Now == nil {
		s.Now = time.Now
	}
	s.bloom = dedup.NewRotating(eps*300, 0.01, 600_000, s.Now().UnixMilli())
	s.inflight = map[string]int{}
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

	// 10. dedup (nothing is recorded until Kafka acknowledged; kept keys are marked in flight)
	valid, keys := s.dedup(ctx, valid, nowMs, &res)
	defer s.release(keys)

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
	if s.Confirmer != nil && len(keys) > 0 {
		s.Confirmer.Remember(keys)
	}
	s.Observer.Accepted(m.OEM, res.Accepted)
	return res, nil
}

func dedupKey(vin string, seq uint64) string {
	return "dedup:" + vin + ":" + strconv.FormatUint(seq, 10)
}

// dedup drops a record when the Bloom filter has maybe seen it AND either (a) an identical record
// from another delivery is being produced right now (in flight), or (b) the confirmer has recorded
// it. It records nothing durable: the caller remembers the kept keys after Kafka acknowledged them,
// and releases the in-flight marks either way (defer s.release).
//
// Why dropping (a) is safe: if the in-flight original's produce fails, its delivery is not
// acknowledged (no MQTT ack / HTTP 503), so its sender retries it; that retry finds neither an
// in-flight mark nor a recorded key and is accepted. If the confirmer fails, everything not in
// flight is kept (degraded; sinks are idempotent).
func (s *Service) dedup(ctx context.Context, recs []canonical.Record, nowMs int64, res *Result) ([]canonical.Record, []string) {
	drop := map[int]bool{}
	var hitIdx []int
	s.mu.Lock()
	for i := range recs {
		k := dedupKey(recs[i].Event.VIN, recs[i].Event.Seq)
		switch {
		case !s.bloom.SeenOrAdd(recs[i].Event.VIN, recs[i].Event.Seq, nowMs):
			s.inflight[k]++ // definitely new
		case s.inflight[k] > 0:
			drop[i] = true // duplicate of a record being produced right now
		default:
			hitIdx = append(hitIdx, i)
		}
	}
	s.mu.Unlock()

	if len(hitIdx) > 0 {
		var seen []bool
		var err error
		if s.Confirmer != nil {
			keys := make([]string, len(hitIdx))
			for j, i := range hitIdx {
				keys[j] = dedupKey(recs[i].Event.VIN, recs[i].Event.Seq)
			}
			seen, err = s.Confirmer.Seen(ctx, keys)
		}
		if s.Confirmer == nil || err != nil || len(seen) != len(hitIdx) {
			s.Observer.DedupDegraded() // no confirmer / Redis down: keep, sinks are idempotent
			seen = make([]bool, len(hitIdx))
		}
		s.mu.Lock()
		for j, i := range hitIdx {
			if seen[j] {
				drop[i] = true
			} else {
				s.inflight[dedupKey(recs[i].Event.VIN, recs[i].Event.Seq)]++
			}
		}
		s.mu.Unlock()
	}

	kept := recs[:0]
	keys := make([]string, 0, len(recs)-len(drop))
	for i, r := range recs {
		if !drop[i] {
			kept = append(kept, r)
			keys = append(keys, dedupKey(r.Event.VIN, r.Event.Seq))
		}
	}
	if len(drop) > 0 {
		res.Duplicates = len(drop)
		s.Observer.Duplicates(len(drop))
	}
	return kept, keys
}

// release clears in-flight marks once a produce finished (successfully or not).
func (s *Service) release(keys []string) {
	if len(keys) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		if s.inflight[k]--; s.inflight[k] <= 0 {
			delete(s.inflight, k)
		}
	}
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
