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

func TestConfirmAndRemember(t *testing.T) {
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
	got, err := c.ConfirmNew(ctx, []string{k, k + ":b"})
	if err != nil || !got[0] || !got[1] {
		t.Fatalf("first sighting must be new: %v %v", got, err)
	}
	if got, _ = c.ConfirmNew(ctx, []string{k}); got[0] {
		t.Fatal("second sighting must be a duplicate")
	}
	r := k + ":remembered"
	c.Remember([]string{r})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := c.ConfirmNew(ctx, []string{r}); !got[0] {
			return // the async batch landed
		}
		// ConfirmNew itself set it if the batch had not landed yet; use a fresh key and retry.
		r = fmt.Sprintf("%s:%d", r, time.Now().UnixNano())
		c.Remember([]string{r})
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("Remember never reached Redis")
}

// The race the pending set exists for: a duplicate confirmed before the async batch has flushed.
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
	got, err := c.ConfirmNew(context.Background(), []string{k, k + ":other"})
	if err != nil || got[0] || !got[1] {
		t.Fatalf("pending key must be a duplicate and an unseen key new: %v %v", got, err)
	}
}

func TestRedisDownFailsFast(t *testing.T) {
	c, err := New("redis://127.0.0.1:1/0", time.Second) // nothing listens on port 1
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	if _, err := c.ConfirmNew(context.Background(), []string{"k"}); err == nil {
		t.Fatal("want error")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %v; the hot path must fail fast", d)
	}
}
