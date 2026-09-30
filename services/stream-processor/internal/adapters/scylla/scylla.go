// Package scylla is the hot raw store (kilowatt.telemetry_raw, cql/0001_telemetry_raw.cql).
// Writes are idempotent upserts on ((vin, day), ts_event_ms, seq) at consistency ONE (root §5.1: AP),
// issued concurrently with a bound. Absent sensor groups and empty DTC lists are bound as UnsetValue,
// not null: binding null writes a tombstone per cell, which (with a non-frozen collection column)
// cut local write throughput to ~2.6K rows/s in the first e2e run.
package scylla

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"

	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/event"
)

const insert = `INSERT INTO kilowatt.telemetry_raw (vin, day, ts_event_ms, seq, ts_ingest_ms, lat, lon, speed_kmh, odo_km, soc_pct,
 pack_voltage_v, pack_current_a, pack_temp_min_c, pack_temp_max_c, cell_v_min_mv, cell_v_max_mv, isolation_kohm,
 hv_interlock_ok, aux_12v_v, ambient_c, charge_state, charge_power_kw, dtc, evt)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

type Store struct {
	s           *gocql.Session
	concurrency int
}

func New(hosts string, concurrency int) (*Store, error) {
	c := gocql.NewCluster(strings.Split(hosts, ",")...)
	c.Consistency = gocql.One
	c.Timeout = 5 * time.Second
	c.ConnectTimeout = 10 * time.Second
	c.NumConns = 2
	s, err := c.CreateSession()
	if err != nil {
		return nil, err
	}
	return &Store{s: s, concurrency: concurrency}, nil
}

func args(e *event.Event) []any {
	u := gocql.UnsetValue
	var lat, lon, speed, tmin, tmax, cmin, cmax, dtc any = u, u, u, u, u, u, u, u
	if e.Has&event.HasGPS != 0 {
		lat, lon, speed = e.Lat, e.Lon, float32(e.SpeedKmh)
	}
	if e.Has&event.HasTemp != 0 {
		tmin, tmax = float32(e.PackTempMinC), float32(e.PackTempMaxC)
	}
	if e.Has&event.HasCells != 0 {
		cmin, cmax = int(e.CellVMinMv), int(e.CellVMaxMv)
	}
	if len(e.DTC) > 0 {
		dtc = e.DTC
	}
	day := time.UnixMilli(e.TsEventMs).UTC().Truncate(24 * time.Hour)
	return []any{e.VIN, day, e.TsEventMs, int64(e.Seq), e.TsIngestMs, lat, lon, speed, e.OdoKm, float32(e.SoCPct),
		float32(e.PackVoltageV), float32(e.PackCurrentA), tmin, tmax, cmin, cmax, float32(e.IsolationKohm),
		e.HVInterlockOK, float32(e.Aux12vV), float32(e.AmbientC), int(e.ChargeState), float32(e.ChargePowerKW), dtc, int(e.Evt)}
}

// Write inserts all events. Failed inserts (typically timeouts under overload) are retried on their
// own, up to 3 attempts with backoff, so an overloaded cluster is not hit again with rows that
// already succeeded. It returns an error only if some rows still failed.
func (st *Store) Write(ctx context.Context, evs []event.Event) error {
	pending := make([]*event.Event, len(evs))
	for i := range evs {
		pending[i] = &evs[i]
	}
	var lastErr error
	for attempt := 0; attempt < 3 && len(pending) > 0; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 250 * time.Millisecond):
			}
		}
		pending, lastErr = st.insertAll(ctx, pending)
	}
	if len(pending) > 0 {
		return fmt.Errorf("%d of %d inserts failed: %w", len(pending), len(evs), lastErr)
	}
	return nil
}

func (st *Store) insertAll(ctx context.Context, evs []*event.Event) ([]*event.Event, error) {
	sem := make(chan struct{}, st.concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failed []*event.Event
	var lastErr error
	for _, e := range evs {
		sem <- struct{}{}
		wg.Add(1)
		go func(e *event.Event) {
			defer func() { <-sem; wg.Done() }()
			if err := st.s.Query(insert, args(e)...).WithContext(ctx).Exec(); err != nil {
				mu.Lock()
				failed, lastErr = append(failed, e), err
				mu.Unlock()
			}
		}(e)
	}
	wg.Wait()
	return failed, lastErr
}

func (st *Store) Ping(ctx context.Context) error {
	return st.s.Query("SELECT now() FROM system.local").WithContext(ctx).Exec()
}

func (st *Store) Close() { st.s.Close() }
