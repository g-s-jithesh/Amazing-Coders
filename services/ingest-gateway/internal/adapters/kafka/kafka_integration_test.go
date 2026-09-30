//go:build integration

package kafka

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/app"
)

func broker() string {
	if b := os.Getenv("KAFKA_BOOTSTRAP"); b != "" {
		return b
	}
	return "localhost:9092"
}

// Produce returns only after acks; records (with headers) are then readable.
func TestProduceWaitsForAcksAndKeepsHeaders(t *testing.T) {
	p, err := NewProducer([]string{broker()}, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := p.Ping(ctx); err != nil {
		t.Skipf("no Kafka at %s: %v", broker(), err)
	}
	key := fmt.Sprintf("IT-%d", time.Now().UnixNano())
	outs := []app.Out{{Topic: app.TopicDLQ, Key: key, Value: []byte("payload"), Headers: []app.Header{{Key: "x-dlq-reason", Value: "RANGE"}}}}
	if err := p.Produce(ctx, outs); err != nil {
		t.Fatal(err)
	}
	if p.Backlog() != 0 {
		t.Fatalf("backlog %d after acked produce", p.Backlog())
	}
	c, err := kgo.NewClient(kgo.SeedBrokers(broker()), kgo.ConsumeTopics(app.TopicDLQ), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for ctx.Err() == nil {
		found := false
		c.PollFetches(ctx).EachRecord(func(r *kgo.Record) {
			if string(r.Key) == key {
				found = len(r.Headers) == 1 && r.Headers[0].Key == "x-dlq-reason" && string(r.Headers[0].Value) == "RANGE" && string(r.Value) == "payload"
			}
		})
		if found {
			return
		}
	}
	t.Fatal("record not found with its headers")
}

func TestProduceToMissingTopicFails(t *testing.T) {
	p, err := NewProducer([]string{broker()}, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Ping(ctx); err != nil {
		t.Skip("no Kafka")
	}
	// auto-create is off in compose: producing to an unknown topic must error, not hang or succeed.
	if err := p.Produce(ctx, []app.Out{{Topic: "does.not.exist.v1", Value: []byte("x")}}); err == nil {
		t.Fatal("want error for a missing topic")
	}
}
