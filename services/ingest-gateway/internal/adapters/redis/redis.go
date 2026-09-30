// Package redis is the exact dedup store behind the Bloom filter: SET dedup:{vin}:{seq} 1 NX EX 600.
// ConfirmNew is one pipelined round trip per message; Remember batches first sightings every
// FlushEvery (never per event) and drops batches if Redis is slow (dedup is best-effort).
//
// Keys remembered but not yet flushed are kept in an in-memory pending set that ConfirmNew checks
// first: a duplicate usually arrives milliseconds after its original, i.e. before the batch lands.
// (Measured: without this, only 48 of ~3,300 simulator duplicates were caught.) Size is bounded by
// eps × flush interval (~5K keys at 100K eps, 50 ms).
package redis

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const ttl = 600 * time.Second

type Confirmer struct {
	rdb     *goredis.Client
	queue   chan []string
	stop    chan struct{}
	done    chan struct{}
	dropped atomic.Int64
	mu      sync.Mutex
	pending map[string]struct{}
}

func New(url string, flushEvery time.Duration) (*Confirmer, error) {
	opt, err := goredis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	opt.ReadTimeout, opt.WriteTimeout, opt.DialTimeout = 200*time.Millisecond, 200*time.Millisecond, time.Second
	c := &Confirmer{rdb: goredis.NewClient(opt), queue: make(chan []string, 4096), stop: make(chan struct{}), done: make(chan struct{}), pending: map[string]struct{}{}}
	go c.loop(flushEvery)
	return c, nil
}

func (c *Confirmer) ConfirmNew(ctx context.Context, keys []string) ([]bool, error) {
	out := make([]bool, len(keys))
	var ask []int // keys not in the pending set go to Redis
	c.mu.Lock()
	for i, k := range keys {
		if _, seen := c.pending[k]; !seen {
			ask = append(ask, i)
		}
	}
	c.mu.Unlock()
	if len(ask) == 0 {
		return out, nil
	}
	pipe := c.rdb.Pipeline()
	cmds := make([]*goredis.BoolCmd, len(ask))
	for j, i := range ask {
		cmds[j] = pipe.SetNX(ctx, keys[i], 1, ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	for j, i := range ask {
		out[i] = cmds[j].Val()
	}
	return out, nil
}

// Remember never blocks: if the buffer is full the keys are dropped (a later duplicate of them
// may then pass, which idempotent sinks absorb).
func (c *Confirmer) Remember(keys []string) {
	// Mark pending before queueing, so the flusher can never delete a key before it was added.
	c.mu.Lock()
	for _, k := range keys {
		c.pending[k] = struct{}{}
	}
	c.mu.Unlock()
	select {
	case c.queue <- keys:
	default:
		c.mu.Lock()
		for _, k := range keys {
			delete(c.pending, k)
		}
		c.mu.Unlock()
		c.dropped.Add(int64(len(keys)))
	}
}

// Dropped counts first-sighting keys never written because Redis was slow or down.
func (c *Confirmer) Dropped() int64 { return c.dropped.Load() }

func (c *Confirmer) loop(every time.Duration) {
	defer close(c.done)
	t := time.NewTicker(every)
	defer t.Stop()
	var batch []string
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		pipe := c.rdb.Pipeline()
		for _, k := range batch {
			pipe.SetNX(ctx, k, 1, ttl)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			c.dropped.Add(int64(len(batch)))
		}
		cancel()
		c.mu.Lock()
		for _, k := range batch {
			delete(c.pending, k)
		}
		c.mu.Unlock()
		batch = batch[:0]
	}
	for {
		select {
		case keys := <-c.queue:
			batch = append(batch, keys...)
			if len(batch) >= 5000 {
				flush()
			}
		case <-t.C:
			flush()
		case <-c.stop:
			flush()
			return
		}
	}
}

func (c *Confirmer) Ping(ctx context.Context) error { return c.rdb.Ping(ctx).Err() }

func (c *Confirmer) Close() error {
	close(c.stop)
	<-c.done
	return c.rdb.Close()
}
