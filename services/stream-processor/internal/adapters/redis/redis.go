// Package redis is the live read model for the map (CQRS read side, rebuildable from vehicle.state.v1):
//
//	vehicle:{vin}:state        HASH  latest state
//	fleet:{fleet_id}:geo       GEO   positions (only when the GPS group is present)
//	fleet:{fleet_id}:deltas    PUB/SUB JSON deltas (the processor throttles to ≤ 1/s per vehicle)
//	topk:{tenant}:{hour}       ZSET  most frequent DTCs raised this hour (Count-Min estimates)
//
// One pipeline per batch. Coordinates are published unmasked here; location masking is enforced by
// fleet-api before anything reaches a client (root §10).
package redis

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/app"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/event"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/sketch"
)

type Live struct{ rdb *goredis.Client }

func New(url string) (*Live, error) {
	opt, err := goredis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	opt.ReadTimeout, opt.WriteTimeout = time.Second, time.Second
	return &Live{rdb: goredis.NewClient(opt)}, nil
}

type delta struct {
	VIN      string   `json:"vin"`
	TsMs     int64    `json:"ts_ms"`
	Lat      *float64 `json:"lat,omitempty"`
	Lon      *float64 `json:"lon,omitempty"`
	SpeedKmh *float64 `json:"speed_kmh,omitempty"`
	SoCPct   float64  `json:"soc_pct"`
	TempMaxC *float64 `json:"pack_temp_max_c,omitempty"`
	Charging bool     `json:"charging"`
	Firing   int      `json:"alerts_firing"`
}

func (l *Live) Update(ctx context.Context, live []app.LiveOut) error {
	pipe := l.rdb.Pipeline()
	for _, u := range live {
		e := u.Latest
		d := delta{VIN: e.VIN, TsMs: e.TsEventMs, SoCPct: e.SoCPct, Charging: e.ChargePowerKW > 0, Firing: u.Firing}
		fields := map[string]any{"ts_ms": e.TsEventMs, "soc_pct": e.SoCPct, "charge_state": e.ChargeState,
			"charge_power_kw": e.ChargePowerKW, "alerts_firing": u.Firing, "fleet_id": u.FleetID, "tenant_id": u.TenantID}
		if e.Has&event.HasGPS != 0 {
			fields["lat"], fields["lon"], fields["speed_kmh"] = e.Lat, e.Lon, e.SpeedKmh
			d.Lat, d.Lon, d.SpeedKmh = &e.Lat, &e.Lon, &e.SpeedKmh
		}
		if e.Has&event.HasTemp != 0 {
			fields["pack_temp_max_c"] = e.PackTempMaxC
			d.TempMaxC = &e.PackTempMaxC
		}
		pipe.HSet(ctx, "vehicle:"+e.VIN+":state", fields)
		if u.FleetID == "" {
			continue // unregistered VIN: no fleet map / channel yet
		}
		if d.Lat != nil {
			pipe.GeoAdd(ctx, "fleet:"+u.FleetID+":geo", &goredis.GeoLocation{Name: e.VIN, Longitude: e.Lon, Latitude: e.Lat})
		}
		b, _ := json.Marshal(d)
		pipe.Publish(ctx, "fleet:"+u.FleetID+":deltas", b)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (l *Live) TopDTCs(ctx context.Context, tenant string, hourStartMs int64, top []sketch.Item) error {
	key := "topk:" + tenant + ":" + strconv.FormatInt(hourStartMs, 10)
	pipe := l.rdb.TxPipeline()
	pipe.Del(ctx, key)
	for _, it := range top {
		pipe.ZAdd(ctx, key, goredis.Z{Score: float64(it.Count), Member: it.Key})
	}
	pipe.Expire(ctx, key, 48*time.Hour)
	_, err := pipe.Exec(ctx)
	return err
}

func (l *Live) Ping(ctx context.Context) error { return l.rdb.Ping(ctx).Err() }

func (l *Live) Close() error { return l.rdb.Close() }
