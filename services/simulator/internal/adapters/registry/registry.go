// Package registry bootstraps the fleet.vehicle.v1 compacted topic (ADR-0006) from the generated
// master data, until fleet-api publishes it through its outbox.
package registry

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	fleetv1 "github.com/g-s-jithesh/Amazing-Coders/libs/proto/gen/go/kilowatt/fleet/v1"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/masterdata"
)

const Topic = "fleet.vehicle.v1"

// Records maps master data to registry messages (key = VIN).
func Records(ds *masterdata.Dataset, models []masterdata.Model) ([]*fleetv1.Vehicle, error) {
	model := map[string]masterdata.Model{}
	for _, m := range models {
		model[m.Code] = m
	}
	depot := map[string]masterdata.Depot{}
	for _, d := range ds.Depots {
		depot[d.ID] = d
	}
	out := make([]*fleetv1.Vehicle, len(ds.Vehicles))
	for i, v := range ds.Vehicles {
		m, ok := model[v.ModelCode]
		d, ok2 := depot[v.HomeDepotID]
		if !ok || !ok2 || ds.Duties[i].VehicleID != v.ID {
			return nil, fmt.Errorf("registry: inconsistent master data for %s", v.VIN)
		}
		out[i] = &fleetv1.Vehicle{
			Vin: v.VIN, TenantId: v.TenantID, FleetId: v.FleetID, DepotId: d.ID, DepotLat: d.Lat, DepotLon: d.Lon,
			ModelCode: m.Code, CapacityKwh: m.CapacityKWh, WhPerKm: float64(m.WhPerKm), NominalVoltageV: m.VoltageV,
			DepartMinIst: int32(ds.Duties[i].DepartMin), ReturnMinIst: int32(ds.Duties[i].ReturnMin),
		}
	}
	return out, nil
}

// Publish writes the registry and returns once every record is acknowledged (or the first error).
func Publish(ctx context.Context, brokers []string, recs []*fleetv1.Vehicle) error {
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerLinger(20*time.Millisecond), kgo.ProducerBatchCompression(kgo.ZstdCompression()))
	if err != nil {
		return err
	}
	defer cl.Close()
	var mu sync.Mutex
	var first error
	for _, r := range recs {
		b, err := proto.Marshal(r)
		if err != nil {
			return err
		}
		cl.Produce(ctx, &kgo.Record{Topic: Topic, Key: []byte(r.Vin), Value: b}, func(_ *kgo.Record, err error) {
			if err != nil {
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
			}
		})
	}
	if err := cl.Flush(ctx); err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	return first
}
