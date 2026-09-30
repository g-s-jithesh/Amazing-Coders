package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/adapters/oem"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/app"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/canonical"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/pipeline"
)

var samples = filepath.Join("..", "..", "..", "..", "libs", "oem-samples")

const goldenVIN = "0KCDV45N9RC000001"

var goldenNow = time.UnixMilli(1788238800123 + 500)

type fakeProducer struct {
	mu   sync.Mutex
	outs []app.Out
	err  error
}

func (f *fakeProducer) Produce(_ context.Context, outs []app.Out) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.outs = append(f.outs, outs...)
	return nil
}

func (f *fakeProducer) topic(t string) []app.Out {
	var r []app.Out
	for _, o := range f.outs {
		if o.Topic == t {
			r = append(r, o)
		}
	}
	return r
}

type fakeConfirmer struct {
	seen       map[string]bool
	err        error
	remembered int
}

func (f *fakeConfirmer) ConfirmNew(_ context.Context, keys []string) ([]bool, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]bool, len(keys))
	for i, k := range keys {
		out[i] = !f.seen[k]
		f.seen[k] = true
	}
	return out, nil
}

func (f *fakeConfirmer) Remember(keys []string) {
	f.remembered += len(keys)
	for _, k := range keys {
		f.seen[k] = true
	}
}

type countObs struct {
	mu                       sync.Mutex
	accepted, dups, degraded int
	rejected                 map[canonical.Reason]int
}

func (o *countObs) Accepted(_ string, n int) { o.mu.Lock(); o.accepted += n; o.mu.Unlock() }
func (o *countObs) Rejected(_ string, r canonical.Reason, n int) {
	o.mu.Lock()
	o.rejected[r] += n
	o.mu.Unlock()
}
func (o *countObs) Duplicates(n int)             { o.dups += n }
func (o *countObs) DedupDegraded()               { o.degraded++ }
func (o *countObs) ProduceLatency(time.Duration) {}

type rawEncoder struct{}

func (rawEncoder) Marshal(e *canonical.Event) ([]byte, error) {
	return []byte(e.VIN + "|" + e.EventID), nil
}

func newSvc(p app.Producer, c app.Confirmer) (*app.Service, *countObs) {
	obs := &countObs{rejected: map[canonical.Reason]int{}}
	dec := map[string]app.Decoder{}
	for id, d := range oem.Registry {
		dec[id] = d
	}
	s := &app.Service{
		Decoders: dec, Chain: pipeline.Default(map[string]string{"0KC": "oem_c", "0KA": "oem_a", "0KB": "oem_b"}),
		Encoder: rawEncoder{}, Producer: p, Observer: obs, Now: func() time.Time { return goldenNow },
	}
	if c != nil {
		s.Confirmer = c
	}
	return app.NewService(1000, s), obs
}

func golden(t *testing.T, oemID, name string) []byte {
	b, err := os.ReadFile(filepath.Join(samples, oemID, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func header(o app.Out, k string) string {
	for _, h := range o.Headers {
		if h.Key == k {
			return h.Value
		}
	}
	return ""
}

func TestValidMessageIsRawPlusCanonical(t *testing.T) {
	p := &fakeProducer{}
	svc, obs := newSvc(p, &fakeConfirmer{seen: map[string]bool{}})
	res, err := svc.Handle(context.Background(), app.Message{OEM: "oem_c", TopicVIN: goldenVIN, Body: golden(t, "oem_c", "valid.txt")})
	if err != nil || res.Accepted != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	raw, can := p.topic("oem.raw.oem_c.v1"), p.topic(app.TopicCanonical)
	if len(raw) != 1 || len(can) != 1 || len(p.topic(app.TopicDLQ)) != 0 || can[0].Key != goldenVIN || raw[0].Key != goldenVIN {
		t.Fatalf("outs %+v", p.outs)
	}
	if !regexp.MustCompile(`^0KCDV45N9RC000001\|[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).Match(can[0].Value) {
		t.Fatalf("event id not a UUIDv7: %s", can[0].Value)
	}
	if obs.accepted != 1 {
		t.Fatalf("observer accepted %d", obs.accepted)
	}
}

// Every malformed golden file lands in the DLQ with the right reason header and its own bytes;
// the raw topic still gets the authenticated message.
func TestMalformedGoldenFilesReachDLQ(t *testing.T) {
	for _, c := range []struct{ file, reason string }{
		{"malformed_vin_checksum.txt", "VIN_CHECKSUM"}, {"malformed_dtc_format.txt", "DTC_FORMAT"},
		{"malformed_schema_version.txt", "UNKNOWN_SCHEMA_VERSION"}, {"malformed_range.txt", "RANGE"},
		{"malformed_decode.txt", "DECODE"},
	} {
		p := &fakeProducer{}
		svc, _ := newSvc(p, nil)
		body := golden(t, "oem_c", c.file)
		topicVIN := goldenVIN
		if c.reason == "VIN_CHECKSUM" {
			topicVIN = strings.Split(string(body), ";")[1] // topic carries the (bad) payload VIN
		}
		if _, err := svc.Handle(context.Background(), app.Message{OEM: "oem_c", TopicVIN: topicVIN, Body: body}); err != nil {
			t.Fatal(err)
		}
		d := p.topic(app.TopicDLQ)
		if len(d) != 1 || header(d[0], "x-dlq-reason") != c.reason || header(d[0], "x-oem") != "oem_c" ||
			header(d[0], "x-received-ms") == "" || header(d[0], "x-error") == "" || string(d[0].Value) != string(body) {
			t.Errorf("%s: dlq %+v", c.file, d)
		}
		if len(p.topic("oem.raw.oem_c.v1")) != 1 || len(p.topic(app.TopicCanonical)) != 0 {
			t.Errorf("%s: raw/canonical wrong", c.file)
		}
	}
}

func TestIdentityMismatchTopicVsPayload(t *testing.T) {
	p := &fakeProducer{}
	svc, _ := newSvc(p, nil)
	_, _ = svc.Handle(context.Background(), app.Message{OEM: "oem_c", TopicVIN: "0KCDV45N9RC000002", Body: golden(t, "oem_c", "valid.txt")})
	if d := p.topic(app.TopicDLQ); len(d) != 1 || header(d[0], "x-dlq-reason") != "IDENTITY_MISMATCH" {
		t.Fatalf("dlq %+v", d)
	}
}

func TestBatchMixedRecords(t *testing.T) {
	good := strings.Trim(string(golden(t, "oem_b", "valid.json")), "[]")
	bad := strings.Trim(string(golden(t, "oem_b", "malformed_range.json")), "[]")
	// oem_b golden VIN has WMI 0KC; register it for oem_b for this test.
	p2 := &fakeProducer{}
	s2, _ := newSvcWMI(p2, map[string]string{"0KC": "oem_b"})
	res, err := s2.Handle(context.Background(), app.Message{OEM: "oem_b", Batch: true, Body: []byte("[" + good + "," + bad + "]")})
	if err != nil || res.Accepted != 1 || res.Rejected[canonical.Range] != 1 {
		t.Fatalf("res %+v err %v", res, err)
	}
	if d := p2.topic(app.TopicDLQ); len(d) != 1 || string(d[0].Value) != bad || d[0].Key != goldenVIN {
		t.Fatalf("dlq must hold only the bad record, keyed by its VIN: %+v", d)
	}
}

func newSvcWMI(p app.Producer, wmi map[string]string) (*app.Service, *countObs) {
	s, obs := newSvc(p, nil)
	s.Chain = pipeline.Default(wmi)
	return s, obs
}

func TestUnknownOEMAndOversize(t *testing.T) {
	p := &fakeProducer{}
	svc, _ := newSvc(p, nil)
	res, _ := svc.Handle(context.Background(), app.Message{OEM: "oem_z", Body: []byte("x")})
	if res.Rejected[canonical.UnknownOEM] != 1 || len(p.topic(app.TopicDLQ)) != 1 || len(p.topic("oem.raw.oem_z.v1")) != 0 {
		t.Fatalf("unknown oem: %+v %+v", res, p.outs)
	}
	p = &fakeProducer{}
	svc, _ = newSvc(p, nil)
	big := make([]byte, app.MaxBody+1)
	res, _ = svc.Handle(context.Background(), app.Message{OEM: "oem_a", Body: big})
	if d := p.topic(app.TopicDLQ); res.Rejected[canonical.Oversize] != 1 || len(d) != 1 || len(d[0].Value) != 64<<10 {
		t.Fatalf("oversize: %+v", res)
	}
	// > 500 records in a batch
	line := strings.TrimSpace(string(golden(t, "oem_c", "valid.txt")))
	body := strings.Repeat(line+"\n", app.MaxBatch+1)
	p = &fakeProducer{}
	svc, _ = newSvc(p, nil)
	res, _ = svc.Handle(context.Background(), app.Message{OEM: "oem_c", Batch: true, Body: []byte(body)})
	if res.Rejected[canonical.Oversize] != 1 || len(p.topic(app.TopicCanonical)) != 0 {
		t.Fatalf("501 records: %+v", res)
	}
}

func TestDedup(t *testing.T) {
	body := golden(t, "oem_c", "valid.txt")
	msg := app.Message{OEM: "oem_c", TopicVIN: goldenVIN, Body: body}

	// True duplicate: Bloom hit, Redis confirms seen → dropped.
	p, conf := &fakeProducer{}, &fakeConfirmer{seen: map[string]bool{}}
	svc, obs := newSvc(p, conf)
	_, _ = svc.Handle(context.Background(), msg)
	res, _ := svc.Handle(context.Background(), msg)
	if res.Duplicates != 1 || res.Accepted != 0 || len(p.topic(app.TopicCanonical)) != 1 || obs.dups != 1 || conf.remembered != 1 {
		t.Fatalf("duplicate: res=%+v canonical=%d remembered=%d", res, len(p.topic(app.TopicCanonical)), conf.remembered)
	}

	// Bloom false positive: Bloom hit but Redis says new → kept (no data loss).
	p, conf = &fakeProducer{}, &fakeConfirmer{seen: map[string]bool{}}
	svc, _ = newSvc(p, conf)
	_, _ = svc.Handle(context.Background(), msg)
	clear(conf.seen) // Redis never saw it (e.g. expired / other replica)
	res, _ = svc.Handle(context.Background(), msg)
	if res.Accepted != 1 || res.Duplicates != 0 {
		t.Fatalf("false positive must be kept: %+v", res)
	}

	// Redis down: keep everything, count degraded.
	p, conf = &fakeProducer{}, &fakeConfirmer{seen: map[string]bool{}, err: errors.New("connection refused")}
	svc, obs = newSvc(p, conf)
	_, _ = svc.Handle(context.Background(), msg)
	res, _ = svc.Handle(context.Background(), msg)
	if res.Accepted != 1 || obs.degraded != 1 {
		t.Fatalf("redis down: res=%+v degraded=%d", res, obs.degraded)
	}

	// No Redis configured at all: Bloom hits are kept, degraded counted.
	p = &fakeProducer{}
	svc, obs = newSvc(p, nil)
	_, _ = svc.Handle(context.Background(), msg)
	res, _ = svc.Handle(context.Background(), msg)
	if res.Accepted != 1 || obs.degraded != 1 {
		t.Fatalf("no redis: res=%+v degraded=%d", res, obs.degraded)
	}
}

// Kafka not acknowledging must surface as an error so the transport withholds the ack / returns 503.
func TestProduceFailurePropagates(t *testing.T) {
	p := &fakeProducer{err: errors.New("NOT_ENOUGH_REPLICAS")}
	svc, obs := newSvc(p, nil)
	if _, err := svc.Handle(context.Background(), app.Message{OEM: "oem_c", TopicVIN: goldenVIN, Body: golden(t, "oem_c", "valid.txt")}); err == nil {
		t.Fatal("want error")
	}
	if obs.accepted != 0 {
		t.Fatal("nothing may be counted as accepted when Kafka failed")
	}
	if err := svc.Reject(context.Background(), app.Message{OEM: "oem_c"}, canonical.Rejectf(canonical.Auth, "x")); err == nil {
		t.Fatal("DLQ produce failure must surface too")
	}
}

func TestUUIDv7(t *testing.T) {
	a, b := app.UUIDv7(1788238800123), app.UUIDv7(1788238800124)
	if a >= b {
		t.Fatalf("UUIDv7 must sort by time: %s >= %s", a, b)
	}
	if a == app.UUIDv7(1788238800123) {
		t.Fatal("random part must differ")
	}
	if app.RawTopic("oem_a") != "oem.raw.oem_a.v1" {
		t.Fatal("raw topic name")
	}
}
