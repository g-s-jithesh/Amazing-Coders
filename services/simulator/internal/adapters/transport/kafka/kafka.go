// Package kafka is the kafka-direct transport, for broker-level load tests only (CLAUDE.md §6):
// it bypasses the gateway and writes raw OEM payloads to oem.raw.<oem>.v1, key = VIN.
// Producer: idempotent, acks=all, zstd, 5 ms linger. Produce blocks when the buffer is full.
package kafka

import (
	"context"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

func Topic(oem string) string { return "oem.raw." + oem + ".v1" }

type Publisher struct {
	cl      *kgo.Client
	errMu   sync.Mutex
	lastErr error
}

func New(brokers []string) (*Publisher, error) {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerLinger(5*time.Millisecond),
		kgo.ProducerBatchCompression(kgo.ZstdCompression()),
		kgo.ProducerBatchMaxBytes(1<<20),
		kgo.MaxBufferedRecords(200_000),
	)
	if err != nil {
		return nil, err
	}
	return &Publisher{cl: cl}, nil
}

func (p *Publisher) Publish(ctx context.Context, oem, vin string, payload []byte) error {
	p.cl.Produce(ctx, &kgo.Record{Topic: Topic(oem), Key: []byte(vin), Value: payload}, func(_ *kgo.Record, err error) {
		if err != nil {
			p.errMu.Lock()
			p.lastErr = err
			p.errMu.Unlock()
		}
	})
	p.errMu.Lock()
	defer p.errMu.Unlock()
	err := p.lastErr
	p.lastErr = nil
	return err
}

// Close flushes buffered records, then closes the client.
func (p *Publisher) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := p.cl.Flush(ctx)
	p.cl.Close()
	if err != nil {
		return err
	}
	p.errMu.Lock()
	defer p.errMu.Unlock()
	return p.lastErr
}
