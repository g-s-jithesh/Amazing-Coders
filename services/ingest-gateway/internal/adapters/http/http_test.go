package http_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	gw "github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/adapters/http"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/adapters/oem"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/app"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/canonical"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/pipeline"
)

var now = time.UnixMilli(1788238800123 + 500)

type prod struct {
	mu   sync.Mutex
	dlq  []string
	n    int
	fail bool
}

func (p *prod) Produce(_ context.Context, outs []app.Out) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail {
		return errors.New("kafka down")
	}
	for _, o := range outs {
		p.n++
		if o.Topic == app.TopicDLQ {
			for _, h := range o.Headers {
				if h.Key == "x-dlq-reason" {
					p.dlq = append(p.dlq, h.Value)
				}
			}
		}
	}
	return nil
}

type nopObs struct{}

func (nopObs) Accepted(string, int)                   {}
func (nopObs) Rejected(string, canonical.Reason, int) {}
func (nopObs) Duplicates(int)                         {}
func (nopObs) DedupDegraded()                         {}
func (nopObs) ProduceLatency(time.Duration)           {}

type enc struct{}

func (enc) Marshal(e *canonical.Event) ([]byte, error) { return []byte(e.VIN), nil }

var secret = []byte("test-secret-value")

func server(p *prod, backlog int64) *httptest.Server {
	dec := map[string]app.Decoder{}
	for id, d := range oem.Registry {
		dec[id] = d
	}
	svc := app.NewService(1000, &app.Service{Decoders: dec, Chain: pipeline.Default(map[string]string{"0KC": "oem_b"}),
		Encoder: enc{}, Producer: p, Observer: nopObs{}, Now: func() time.Time { return now }})
	s := &gw.Server{
		Svc: svc, Now: func() time.Time { return now }, HighWater: 100, Backlog: func() int64 { return backlog },
		Keys: map[string]gw.Key{"sim": {Secret: secret, OEMs: map[string]bool{"oem_b": true, "oem_z": true}}},
		Ready: func(context.Context) error {
			if p.fail {
				return errors.New("down")
			}
			return nil
		},
	}
	return httptest.NewServer(s.Routes())
}

func post(t *testing.T, url, oemID string, body []byte, key string, ts time.Time, sign []byte) *http.Response {
	req, _ := http.NewRequest(http.MethodPost, url+"/ingest/v1/"+oemID+"/batch", bytes.NewReader(body))
	tss := strconv.FormatInt(ts.Unix(), 10)
	req.Header.Set("X-Kw-Key", key)
	req.Header.Set("X-Kw-Timestamp", tss)
	req.Header.Set("X-Kw-Signature", gw.Sign(sign, tss, body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func validBatch(t *testing.T) []byte {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "libs", "oem-samples", "oem_b", "valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBatchStatusCodes(t *testing.T) {
	body := validBatch(t)
	cases := []struct {
		name    string
		oem     string
		body    []byte
		key     string
		ts      time.Time
		sign    []byte
		want    int
		dlq     string
		backlog int64
		kafka   bool // true = kafka down
	}{
		{"ok", "oem_b", body, "sim", now, secret, 200, "", 0, false},
		{"bad signature", "oem_b", body, "sim", now, []byte("wrong"), 401, "AUTH", 0, false},
		{"unknown key", "oem_b", body, "nobody", now, secret, 401, "AUTH", 0, false},
		{"replay: old timestamp", "oem_b", body, "sim", now.Add(-6 * time.Minute), secret, 401, "AUTH", 0, false},
		{"future timestamp", "oem_b", body, "sim", now.Add(6 * time.Minute), secret, 401, "AUTH", 0, false},
		{"key not for oem_a", "oem_a", body, "sim", now, secret, 403, "IDENTITY_MISMATCH", 0, false},
		{"unknown oem", "oem_z", body, "sim", now, secret, 404, "UNKNOWN_OEM", 0, false},
		{"oversize", "oem_b", make([]byte, app.MaxBody+1), "sim", now, secret, 413, "OVERSIZE", 0, false},
		{"back-pressure", "oem_b", body, "sim", now, secret, 503, "", 101, false},
		{"kafka down", "oem_b", body, "sim", now, secret, 503, "", 0, true},
	}
	for _, c := range cases {
		p := &prod{fail: c.kafka}
		srv := server(p, c.backlog)
		resp := post(t, srv.URL, c.oem, c.body, c.key, c.ts, c.sign)
		if resp.StatusCode != c.want {
			t.Errorf("%s: status %d, want %d", c.name, resp.StatusCode, c.want)
		}
		if c.want == 503 && resp.Header.Get("Retry-After") == "" {
			t.Errorf("%s: 503 without Retry-After", c.name)
		}
		if c.dlq != "" && (len(p.dlq) != 1 || p.dlq[0] != c.dlq) {
			t.Errorf("%s: dlq %v, want [%s]", c.name, p.dlq, c.dlq)
		}
		if c.name == "back-pressure" && p.n != 0 {
			t.Errorf("back-pressure must not touch Kafka")
		}
		srv.Close()
	}
}

func TestHealthAndReadiness(t *testing.T) {
	for _, c := range []struct {
		backlog int64
		kafka   bool
		want    int
	}{{0, false, 200}, {101, false, 503}, {0, true, 503}} {
		srv := server(&prod{fail: c.kafka}, c.backlog)
		r, _ := http.Get(srv.URL + "/readyz")
		h, _ := http.Get(srv.URL + "/healthz")
		if r.StatusCode != c.want || h.StatusCode != 200 {
			t.Errorf("backlog=%d kafka_down=%v: readyz %d healthz %d", c.backlog, c.kafka, r.StatusCode, h.StatusCode)
		}
		srv.Close()
	}
}
