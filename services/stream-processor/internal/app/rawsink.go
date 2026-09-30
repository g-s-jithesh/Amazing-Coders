package app

import (
	"context"
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/event"
)

// RawSink is the raw-store role: it writes canonical events to the hot raw store (Scylla) from its
// own consumer group, retrying until the write succeeds (back-pressure, never skipping). The caller
// commits offsets only after Write returns nil. Writes are idempotent upserts, so redelivery is safe.
type RawSink struct {
	Store RawStore
	Obs   Observer
}

func (s *RawSink) Write(ctx context.Context, evs []event.Event) error {
	if len(evs) == 0 {
		return nil
	}
	start := time.Now()
	err := retry(ctx, s.Obs, "scylla", func() error { return s.Store.Write(ctx, evs) })
	s.Obs.SinkTime("scylla", time.Since(start))
	if err == nil {
		s.Obs.Batch(&Outputs{Raw: evs}, 0) // counts the rows this role stored (sp_events_total)
	}
	return err
}
