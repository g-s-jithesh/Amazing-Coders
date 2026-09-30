//go:build integration

package redis

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func url() string {
	if u := os.Getenv("REDIS_URL"); u != "" {
		return u
	}
	return "redis://localhost:6379/0"
}

func TestSeenIsReadOnlyAndRememberRecords(t *testing.T) {
	c, err := New(url(), 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	if err := c.Ping(ctx); err != nil {
		t.Skipf("no Redis: %v", err)
	}
	k := fmt.Sprintf("dedup:IT:%d", time.Now().UnixNano())
	for i := 0; i < 2; i++ { // asking twice must not record anything
		got, err := c.Seen(ctx, []string{k})
		if err != nil || got[0] {
			t.Fatalf("Seen must be read-only: %v %v", got, err)
		}
	}
	c.Remember([]string{k})
	if got, _ := c.Seen(ctx, []string{k}); !got[0] {
		t.Fatal("a remembered key is seen immediately (pending set)")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n, _ := c.rdb.Exists(ctx, k).Result(); n == 1 {
			return // the async batch reached Redis
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("Remember never reached Redis")
}

// The race the pending set exists for: a duplicate checked before the async batch has flushed.
func TestDuplicateBeforeFlushIsCaught(t *testing.T) {
	c, err := New(url(), time.Hour) // never flushes during the test
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Ping(context.Background()); err != nil {
		t.Skipf("no Redis: %v", err)
	}
	k := fmt.Sprintf("dedup:IT:race:%d", time.Now().UnixNano())
	c.Remember([]string{k})
	got, err := c.Seen(context.Background(), []string{k, k + ":other"})
	if err != nil || !got[0] || got[1] {
		t.Fatalf("pending key must be seen and an unseen key not: %v %v", got, err)
	}
}

func TestRedisDownFailsFast(t *testing.T) {
	c, err := New("redis://127.0.0.1:1/0", time.Second) // nothing listens on port 1
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	if _, err := c.Seen(context.Background(), []string{"k"}); err == nil {
		t.Fatal("want error")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %v; the hot path must fail fast", d)
	}
}
