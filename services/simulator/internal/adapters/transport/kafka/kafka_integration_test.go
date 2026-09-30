//go:build integration

package kafka

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Needs Kafka with the oem.raw.* topics: `make up`, then go test -tags integration ./...
func TestProduceIsConsumable(t *testing.T) {
	broker := os.Getenv("KAFKA_BOOTSTRAP")
	if broker == "" {
		broker = "localhost:9092"
	}
	p, err := New([]string{broker})
	if err != nil {
		t.Fatal(err)
	}
	vin := fmt.Sprintf("IT%015d", time.Now().UnixNano()%1e15)
	const n = 500
	for i := 0; i < n; i++ {
		if err := p.Publish(context.Background(), "oem_a", vin, []byte(fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Close(); err != nil {
		t.Skipf("no broker at %s: %v", broker, err)
	}

	c, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ConsumeTopics(Topic("oem_a")), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	seen, next := 0, 0
	for seen < n && ctx.Err() == nil {
		c.PollFetches(ctx).EachRecord(func(r *kgo.Record) {
			if string(r.Key) != vin {
				return
			}
			if string(r.Value) != fmt.Sprint(next) {
				t.Errorf("order broken: got %s want %d", r.Value, next)
			}
			next++
			seen++
		})
	}
	if seen != n {
		t.Fatalf("consumed %d of %d", seen, n)
	}
}
