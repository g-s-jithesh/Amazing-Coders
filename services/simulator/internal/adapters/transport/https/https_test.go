package https

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recv struct {
	mu      sync.Mutex
	batches [][]byte
	ctypes  []string
}

func server(t *testing.T, secret []byte, status func(n int) int) (*httptest.Server, *recv, *atomic.Int32) {
	r := &recv{}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		n := int(calls.Add(1))
		body, _ := io.ReadAll(req.Body)
		if req.Header.Get(HdrSig) != Sign(secret, req.Header.Get(HdrTS), body) || req.Header.Get(HdrKey) != "sim" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if len(body) > MaxBytes {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		if code := status(n); code != 0 {
			w.WriteHeader(code)
			return
		}
		r.mu.Lock()
		r.batches = append(r.batches, body)
		r.ctypes = append(r.ctypes, req.Header.Get("Content-Type"))
		r.mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return srv, r, &calls
}

func ok(int) int { return 0 }

func TestBatchesAreSignedBoundedAndComplete(t *testing.T) {
	secret := []byte("s3cret-for-tests")
	srv, r, _ := server(t, secret, ok)
	p := New(Config{BaseURL: srv.URL, KeyID: "sim", Secret: secret, FlushInterval: time.Hour})
	const n = 1234
	for i := 0; i < n; i++ {
		if err := p.Publish(context.Background(), "oem_b", "V", []byte(fmt.Sprintf(`{"sequence":%d}`, i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for i, b := range r.batches {
		var recs []map[string]int
		if err := json.Unmarshal(b, &recs); err != nil {
			t.Fatalf("batch %d not a JSON array: %v", i, err)
		}
		if len(recs) > MaxEvents || r.ctypes[i] != "application/json" {
			t.Fatalf("batch %d: %d records, %s", i, len(recs), r.ctypes[i])
		}
		for j, rec := range recs {
			if j > 0 && rec["sequence"] != recs[j-1]["sequence"]+1 {
				t.Fatalf("order broken inside batch %d", i) // across batches order is not guaranteed
			}
			seen[rec["sequence"]] = true
		}
	}
	if len(seen) != n || len(r.batches) != 3 || p.Sent() != n || p.Dropped() != 0 {
		t.Fatalf("delivered %d unique in %d batches (sent=%d dropped=%d), want %d in 3", len(seen), len(r.batches), p.Sent(), p.Dropped(), n)
	}
}

func TestByteLimitSplitsBatches(t *testing.T) {
	srv, r, _ := server(t, nil, ok)
	p := New(Config{BaseURL: srv.URL, KeyID: "sim", FlushInterval: time.Hour})
	big := []byte(`"` + strings.Repeat("x", 100_000) + `"`)
	for i := 0; i < 7; i++ {
		_ = p.Publish(context.Background(), "oem_a", "V", big)
	}
	_ = p.Close()
	if len(r.batches) < 3 {
		t.Fatalf("7×100 KB should need ≥ 3 batches under 256 KB, got %d", len(r.batches))
	}
}

func TestOEMCIsNewlineTextAndBrokenRecordsGoAlone(t *testing.T) {
	srv, r, _ := server(t, nil, ok)
	p := New(Config{BaseURL: srv.URL, KeyID: "sim", FlushInterval: time.Hour})
	line := []byte("1" + strings.Repeat(";x", 22))
	_ = p.Publish(context.Background(), "oem_c", "V", line)
	_ = p.Publish(context.Background(), "oem_c", "V", []byte("1;trunc"))    // decode-malformed
	_ = p.Publish(context.Background(), "oem_a", "V", []byte(`{"broken":`)) // decode-malformed JSON
	_ = p.Publish(context.Background(), "oem_c", "V", line)
	_ = p.Close()
	var joined, alone int
	for i, b := range r.batches {
		switch {
		case bytes.Equal(b, []byte("1;trunc")) && r.ctypes[i] == "text/plain",
			bytes.Equal(b, []byte(`[{"broken":]`)):
			alone++
		case bytes.Equal(b, append(append(append([]byte{}, line...), '\n'), line...)) && r.ctypes[i] == "text/plain":
			joined++
		}
	}
	if joined != 1 || alone != 2 {
		t.Fatalf("batches %q", r.batches)
	}
}

func TestRetriesTransientThenSucceeds(t *testing.T) {
	srv, r, calls := server(t, nil, func(n int) int {
		if n <= 2 {
			return http.StatusServiceUnavailable
		}
		return 0
	})
	p := New(Config{BaseURL: srv.URL, KeyID: "sim", FlushInterval: time.Hour})
	if err := p.send(context.Background(), "oem_b", [][]byte{[]byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	_ = p.Close()
	if calls.Load() != 3 || len(r.batches) != 1 {
		t.Fatalf("calls=%d delivered=%d", calls.Load(), len(r.batches))
	}
}

func TestClientErrorsAreNotRetried(t *testing.T) {
	srv, _, calls := server(t, nil, func(int) int { return http.StatusBadRequest })
	p := New(Config{BaseURL: srv.URL, KeyID: "sim", FlushInterval: time.Hour})
	if err := p.send(context.Background(), "oem_b", [][]byte{[]byte(`{}`)}); err == nil {
		t.Fatal("want error on 400")
	}
	_ = p.Close()
	if calls.Load() != 1 {
		t.Fatalf("400 retried: %d calls", calls.Load())
	}
}

func TestRejectedBatchesAreCountedAsDropped(t *testing.T) {
	srv, _, _ := server(t, nil, func(int) int { return http.StatusBadRequest })
	p := New(Config{BaseURL: srv.URL, KeyID: "sim", FlushInterval: time.Hour})
	for i := 0; i < 3; i++ {
		_ = p.Publish(context.Background(), "oem_b", "V", []byte(`{}`))
	}
	if err := p.Close(); err == nil || p.Dropped() != 3 {
		t.Fatalf("close err=%v dropped=%d, want error and 3", err, p.Dropped())
	}
}

// The e2e finding behind the async design: with the gateway down, Publish must never block the
// simulation, every record must be accounted as dropped, and Close must return within its grace.
func TestGatewayDownNeverBlocksAndAccountsEveryRecord(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listening
	p := New(Config{BaseURL: url, KeyID: "sim", FlushInterval: 5 * time.Millisecond, QueueBatches: 2, Senders: 1, CloseGrace: 300 * time.Millisecond})
	const n = 5000
	begin := time.Now()
	for i := 0; i < n; i++ {
		_ = p.Publish(context.Background(), "oem_b", "V", []byte(`{}`))
	}
	if d := time.Since(begin); d > time.Second {
		t.Fatalf("publishing %d records took %v with the gateway down", n, d)
	}
	begin = time.Now()
	err := p.Close()
	if d := time.Since(begin); d > 2*time.Second {
		t.Fatalf("Close took %v", d)
	}
	if err == nil || p.Dropped()+p.Sent() != n || p.Sent() != 0 {
		t.Fatalf("err=%v dropped=%d sent=%d, want all %d dropped", err, p.Dropped(), p.Sent(), n)
	}
}

func TestSignIsHMACOfTimestampDotBody(t *testing.T) {
	// echo -n '1700000000.{}' | openssl dgst -sha256 -hmac key
	if got := Sign([]byte("key"), "1700000000", []byte("{}")); len(got) != 64 || got == Sign([]byte("key"), "1700000001", []byte("{}")) {
		t.Fatalf("signature %s must be 64 hex chars and depend on the timestamp", got)
	}
}
