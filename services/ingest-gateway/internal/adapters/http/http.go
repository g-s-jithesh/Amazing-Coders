// Package http is the HTTPS batch webhook (OEM-cloud push) plus health, readiness and metrics.
//
//	POST /ingest/v1/{oem}/batch   X-Kw-Key, X-Kw-Timestamp (unix s), X-Kw-Signature = hex HMAC-SHA256(secret, ts + "." + body)
//	GET  /healthz  /readyz  /metrics
//
// Status codes: 200 handled (records may still be in the DLQ; see body), 401 AUTH, 403 key not
// allowed for this OEM, 404 unknown OEM, 413 oversize, 503 + Retry-After under back-pressure or if
// Kafka did not acknowledge (nothing was accepted; the client must retry).
package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/app"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/canonical"
)

const MaxSkew = 5 * time.Minute

// Key is one OEM-cloud API key.
type Key struct {
	Secret []byte
	OEMs   map[string]bool
}

type Server struct {
	Svc          *app.Service
	Keys         map[string]Key
	Backlog      func() int64 // producer records not yet acknowledged
	HighWater    int64
	Ready        func(ctx context.Context) error
	Metrics      http.Handler
	Backpressure func(active bool)
	Now          func() time.Time
}

func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ingest/v1/{oem}/batch", s.batch)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /readyz", s.readyz)
	if s.Metrics != nil {
		mux.Handle("GET /metrics", s.Metrics)
	}
	return mux
}

func (s *Server) overloaded() bool {
	over := s.Backlog != nil && s.Backlog() > s.HighWater
	if s.Backpressure != nil {
		s.Backpressure(over)
	}
	return over
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if s.overloaded() {
		http.Error(w, "back-pressure", http.StatusServiceUnavailable)
		return
	}
	if s.Ready != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.Ready(ctx); err != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
	}
	_, _ = w.Write([]byte("ready"))
}

func unavailable(w http.ResponseWriter, why string) {
	w.Header().Set("Retry-After", "1")
	http.Error(w, why, http.StatusServiceUnavailable)
}

// Sign is the HMAC the sender must present.
func Sign(secret []byte, ts string, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(ts))
	m.Write([]byte{'.'})
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Server) authenticate(r *http.Request, body []byte) (Key, *canonical.Reject) {
	k, ok := s.Keys[r.Header.Get("X-Kw-Key")]
	if !ok {
		return Key{}, canonical.Rejectf(canonical.Auth, "unknown key")
	}
	ts := r.Header.Get("X-Kw-Timestamp")
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return Key{}, canonical.Rejectf(canonical.Auth, "bad timestamp")
	}
	if skew := s.Now().Sub(time.Unix(sec, 0)); skew > MaxSkew || skew < -MaxSkew {
		return Key{}, canonical.Rejectf(canonical.Auth, "timestamp skew %v (replay?)", skew.Round(time.Second))
	}
	got, err := hex.DecodeString(r.Header.Get("X-Kw-Signature"))
	want, _ := hex.DecodeString(Sign(k.Secret, ts, body))
	if err != nil || !hmac.Equal(got, want) {
		return Key{}, canonical.Rejectf(canonical.Auth, "bad signature")
	}
	return k, nil
}

func (s *Server) batch(w http.ResponseWriter, r *http.Request) {
	if s.overloaded() {
		unavailable(w, "back-pressure")
		return
	}
	oem := r.PathValue("oem")
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, app.MaxBody))
	msg := app.Message{OEM: oem, Body: body, Batch: true}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		s.rejectWith(w, r.Context(), msg, canonical.Rejectf(canonical.Oversize, "body > %d bytes", app.MaxBody), http.StatusRequestEntityTooLarge)
		return
	}
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	key, rej := s.authenticate(r, body)
	if rej != nil {
		s.rejectWith(w, r.Context(), msg, rej, http.StatusUnauthorized)
		return
	}
	if !key.OEMs[oem] {
		s.rejectWith(w, r.Context(), msg, canonical.Rejectf(canonical.IdentityMismatch, "key not allowed for %s", oem), http.StatusForbidden)
		return
	}
	res, err := s.Svc.Handle(r.Context(), msg)
	if err != nil {
		slog.Warn("kafka did not acknowledge batch", "oem", oem, "err", err)
		unavailable(w, "kafka unavailable")
		return
	}
	if res.Rejected[canonical.UnknownOEM] > 0 {
		http.Error(w, "unknown oem", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"accepted": res.Accepted, "duplicates": res.Duplicates, "rejected": res.Rejected})
}

func (s *Server) rejectWith(w http.ResponseWriter, ctx context.Context, m app.Message, rej *canonical.Reject, status int) {
	if err := s.Svc.Reject(ctx, m, rej); err != nil {
		unavailable(w, "kafka unavailable")
		return
	}
	http.Error(w, string(rej.Reason), status)
}
