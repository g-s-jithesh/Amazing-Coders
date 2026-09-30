// Package kafka is the gateway's Producer (franz-go) and canonical protobuf Encoder.
// Producer: idempotent, acks=all, zstd, 5 ms linger, ≤ 1 MB batches, key = VIN.
package kafka

import (
	"context"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	telemetryv1 "github.com/g-s-jithesh/Amazing-Coders/libs/proto/gen/go/kilowatt/telemetry/v1"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/app"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/canonical"
)

type Producer struct{ cl *kgo.Client }

func NewProducer(brokers []string, maxBuffered int) (*Producer, error) {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerLinger(5*time.Millisecond),
		kgo.ProducerBatchCompression(kgo.ZstdCompression()),
		kgo.ProducerBatchMaxBytes(1<<20),
		kgo.MaxBufferedRecords(maxBuffered),
		kgo.RecordDeliveryTimeout(30*time.Second),
	)
	if err != nil {
		return nil, err
	}
	return &Producer{cl: cl}, nil
}

// Produce writes all outs and waits for every acknowledgement; the first error is returned.
func (p *Producer) Produce(ctx context.Context, outs []app.Out) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	wg.Add(len(outs))
	for _, o := range outs {
		r := &kgo.Record{Topic: o.Topic, Value: o.Value}
		if o.Key != "" {
			r.Key = []byte(o.Key)
		}
		for _, h := range o.Headers {
			r.Headers = append(r.Headers, kgo.RecordHeader{Key: h.Key, Value: []byte(h.Value)})
		}
		p.cl.Produce(ctx, r, func(_ *kgo.Record, err error) {
			if err != nil {
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
			}
			wg.Done()
		})
	}
	wg.Wait()
	return first
}

// Backlog is the number of records buffered but not yet acknowledged (for back-pressure).
func (p *Producer) Backlog() int64 { return p.cl.BufferedProduceRecords() }

// Ping reports whether a broker answers (readiness).
func (p *Producer) Ping(ctx context.Context) error { return p.cl.Ping(ctx) }

func (p *Producer) Close(ctx context.Context) error {
	err := p.cl.Flush(ctx)
	p.cl.Close()
	return err
}

// ProtoEncoder marshals canonical events to telemetry.v1.TelemetryEvent.
type ProtoEncoder struct{}

func (ProtoEncoder) Marshal(e *canonical.Event) ([]byte, error) { return proto.Marshal(ToProto(e)) }

// ToProto maps the domain event to the wire contract; absent sensor groups stay unset.
func ToProto(e *canonical.Event) *telemetryv1.TelemetryEvent {
	m := &telemetryv1.TelemetryEvent{
		EventId: e.EventID, Vin: e.VIN, Oem: e.OEM, TsEventMs: e.TsEventMs, TsIngestMs: e.TsIngestMs, Seq: e.Seq,
		OdoKm: e.OdoKm, SocPct: e.SoCPct, PackVoltageV: e.PackVoltageV, PackCurrentA: e.PackCurrentA,
		IsolationKohm: e.IsolationKohm, HvInterlockOk: e.HVInterlockOK, Aux_12VV: e.Aux12vV, AmbientC: e.AmbientC,
		ChargeState: telemetryv1.ChargeState(e.ChargeState), ChargePowerKw: e.ChargePowerKW, Dtc: e.DTC,
		Evt: telemetryv1.EventType(e.Evt), SchemaVersion: e.SchemaVersion,
	}
	if e.Has&canonical.HasGPS != 0 {
		m.Lat, m.Lon, m.SpeedKmh = proto.Float64(e.Lat), proto.Float64(e.Lon), proto.Float32(e.SpeedKmh)
	}
	if e.Has&canonical.HasTemp != 0 {
		m.PackTempMinC, m.PackTempMaxC = proto.Float32(e.PackTempMinC), proto.Float32(e.PackTempMaxC)
	}
	if e.Has&canonical.HasCells != 0 {
		m.CellVMinMv, m.CellVMaxMv = proto.Uint32(e.CellVMinMv), proto.Uint32(e.CellVMaxMv)
	}
	return m
}
