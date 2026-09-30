// Package https pushes OEM payloads to the ingest-gateway batch webhook, the way an OEM cloud
// would: POST {base}/ingest/v1/{oem}/batch, ≤ MaxEvents records and ≤ MaxBytes per body, signed
// with HMAC-SHA256 over "<unix-seconds>.<body>" (gateway contract in services/ingest-gateway/CLAUDE.md).
//
// JSON OEMs are sent as a JSON array; oem_c as newline-separated lines. A payload that is not
// valid on its own (the decode-malformed noise case) is sent in a batch by itself so it does not
// poison valid records.
//
// Sending is asynchronous: full batches go to a bounded queue drained by sender goroutines, so a
// slow or absent gateway never stalls the simulation (an OEM-B outage must not slow OEM-A
// vehicles). When the queue is full, or a batch fails after retries, its records are counted in
// Dropped. Batches may be delivered out of order across senders; the pipeline handles that.
package https

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	MaxEvents   = 500
	MaxBytes    = 256 << 10
	HdrTS       = "X-Kw-Timestamp"
	HdrSig      = "X-Kw-Signature"
	HdrKey      = "X-Kw-Key"
	maxAttempts = 4
)

// ErrQueueFull is recorded when batches arrive faster than the gateway accepts them.
var ErrQueueFull = errors.New("https: send queue full, batch dropped")

// Sign returns the hex HMAC-SHA256 of "<ts>.<body>".
func Sign(secret []byte, ts string, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(ts))
	m.Write([]byte{'.'})
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

type Config struct {
	BaseURL       string // http://localhost:8081
	KeyID         string // API key id sent in X-Kw-Key
	Secret        []byte
	FlushInterval time.Duration
	Senders       int           // concurrent POSTs (default 4)
	QueueBatches  int           // bounded send queue (default 256)
	CloseGrace    time.Duration // how long Close waits for in-flight retries (default 5 s)
	Client        *http.Client
	Now           func() time.Time // for tests
}

type batch struct {
	recs  [][]byte
	bytes int
}

type job struct {
	oem  string
	recs [][]byte
}

type Publisher struct {
	cfg     Config
	mu      sync.Mutex
	open    map[string]*batch
	q       chan job
	stop    chan struct{}
	senders sync.WaitGroup
	loopWG  sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
	dropped atomic.Int64
	sent    atomic.Int64
	errMu   sync.Mutex
	lastErr error
}

func New(cfg Config) *Publisher {
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 15 * time.Second}
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = time.Second
	}
	if cfg.Senders < 1 {
		cfg.Senders = 4
	}
	if cfg.QueueBatches < 1 {
		cfg.QueueBatches = 256
	}
	if cfg.CloseGrace <= 0 {
		cfg.CloseGrace = 5 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Publisher{cfg: cfg, open: map[string]*batch{}, q: make(chan job, cfg.QueueBatches), stop: make(chan struct{}), ctx: ctx, cancel: cancel}
	for i := 0; i < cfg.Senders; i++ {
		p.senders.Add(1)
		go p.sender()
	}
	p.loopWG.Add(1)
	go p.loop()
	return p
}

func (p *Publisher) sender() {
	defer p.senders.Done()
	for j := range p.q {
		if err := p.send(p.ctx, j.oem, j.recs); err != nil {
			p.fail(len(j.recs), err)
		} else {
			p.sent.Add(int64(len(j.recs)))
		}
	}
}

func (p *Publisher) loop() {
	defer p.loopWG.Done()
	t := time.NewTicker(p.cfg.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			p.flushAll()
		case <-p.stop:
			p.flushAll()
			close(p.q)
			return
		}
	}
}

func (p *Publisher) fail(n int, err error) {
	p.dropped.Add(int64(n))
	p.errMu.Lock()
	p.lastErr = err
	p.errMu.Unlock()
}

func (p *Publisher) enqueue(oem string, recs [][]byte) {
	select {
	case p.q <- job{oem, recs}:
	default:
		p.fail(len(recs), ErrQueueFull)
	}
}

func isJSON(oem string) bool { return oem != "oem_c" }

// selfContained reports whether a record can share a batch (it parses on its own).
func selfContained(oem string, rec []byte) bool {
	if isJSON(oem) {
		return json.Valid(rec)
	}
	return bytes.Count(rec, []byte{';'}) == 22
}

// Publish buffers the record; it never blocks on the network. Delivery failures show up in Dropped.
func (p *Publisher) Publish(_ context.Context, oem, _ string, payload []byte) error {
	if !selfContained(oem, payload) {
		p.enqueue(oem, [][]byte{payload})
		return nil
	}
	p.mu.Lock()
	b := p.open[oem]
	if b == nil {
		b = &batch{}
		p.open[oem] = b
	}
	var full *batch
	if len(b.recs) > 0 && (len(b.recs) >= MaxEvents || b.bytes+len(payload)+2 > MaxBytes) {
		full, b = b, &batch{}
		p.open[oem] = b
	}
	b.recs = append(b.recs, payload)
	b.bytes += len(payload) + 1
	p.mu.Unlock()
	if full != nil {
		p.enqueue(oem, full.recs)
	}
	return nil
}

func (p *Publisher) flushAll() {
	p.mu.Lock()
	open := p.open
	p.open = map[string]*batch{}
	p.mu.Unlock()
	for oem, b := range open {
		if len(b.recs) > 0 {
			p.enqueue(oem, b.recs)
		}
	}
}

func (p *Publisher) body(oem string, recs [][]byte) ([]byte, string) {
	var buf bytes.Buffer
	if isJSON(oem) {
		buf.WriteByte('[')
		for i, r := range recs {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.Write(r)
		}
		buf.WriteByte(']')
		return buf.Bytes(), "application/json"
	}
	for i, r := range recs {
		if i > 0 {
			buf.WriteByte('\n')
		}
		buf.Write(r)
	}
	return buf.Bytes(), "text/plain"
}

// send POSTs one batch, retrying transport errors, 429 and 5xx with Retry-After or exponential backoff.
func (p *Publisher) send(ctx context.Context, oem string, recs [][]byte) error {
	body, ctype := p.body(oem, recs)
	url := p.cfg.BaseURL + "/ingest/v1/" + oem + "/batch"
	backoff := 200 * time.Millisecond
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		ts := strconv.FormatInt(p.cfg.Now().Unix(), 10)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", ctype)
		req.Header.Set(HdrKey, p.cfg.KeyID)
		req.Header.Set(HdrTS, ts)
		req.Header.Set(HdrSig, Sign(p.cfg.Secret, ts, body))
		resp, err := p.cfg.Client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			switch {
			case resp.StatusCode < 300:
				return nil
			case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
				lastErr = fmt.Errorf("https: %s → %d", url, resp.StatusCode)
				if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
					backoff = time.Duration(s) * time.Second
				}
			default: // 4xx other than 429: the gateway rejected the batch; retrying will not help
				return fmt.Errorf("https: %s → %d (%d records)", url, resp.StatusCode, len(recs))
			}
		}
		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
	}
	return fmt.Errorf("https: giving up after %d attempts (%d records): %w", maxAttempts, len(recs), lastErr)
}

// Dropped is the number of records that could not be delivered (queue full or send failed).
func (p *Publisher) Dropped() int64 { return p.dropped.Load() }

// Sent is the number of records the gateway accepted (2xx).
func (p *Publisher) Sent() int64 { return p.sent.Load() }

// Close flushes open batches, waits up to CloseGrace for senders, then aborts what is left.
// It returns the last delivery error, if any records were dropped.
func (p *Publisher) Close() error {
	close(p.stop)
	p.loopWG.Wait()
	done := make(chan struct{})
	go func() { p.senders.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(p.cfg.CloseGrace):
		p.cancel()
		<-done
	}
	p.cancel()
	if n := p.dropped.Load(); n > 0 {
		p.errMu.Lock()
		defer p.errMu.Unlock()
		return fmt.Errorf("https: %d records dropped; last error: %w", n, p.lastErr)
	}
	return nil
}
