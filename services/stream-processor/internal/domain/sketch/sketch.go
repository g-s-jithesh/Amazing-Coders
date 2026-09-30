// Package sketch counts DTC occurrences per tenant in bounded memory: a Count-Min Sketch estimates
// frequencies (never under-counts; over-counts by ≤ ε·N with probability ≥ 1−δ, where
// width = ⌈e/ε⌉ and depth = ⌈ln(1/δ)⌉), and a min-heap keeps the current top-K candidates.
// Add: O(d + log K); Top: O(K log K).
package sketch

import (
	"container/heap"
	"hash/fnv"
	"math"
	"sort"
)

type CMS struct {
	w, d  int
	rows  [][]uint32
	seeds []uint64
	N     uint64
}

// NewCMS sizes the sketch for error ε (fraction of N) with failure probability δ.
func NewCMS(eps, delta float64) *CMS {
	w := int(math.Ceil(math.E / eps))
	d := int(math.Ceil(math.Log(1 / delta)))
	c := &CMS{w: w, d: d, rows: make([][]uint32, d), seeds: make([]uint64, d)}
	for i := range c.rows {
		c.rows[i] = make([]uint32, w)
		c.seeds[i] = uint64(i)*0x9E3779B97F4A7C15 + 1
	}
	return c
}

func (c *CMS) idx(key string, i int) int {
	h := fnv.New64a()
	var b [8]byte
	for j := range b {
		b[j] = byte(c.seeds[i] >> (8 * j))
	}
	_, _ = h.Write(b[:])
	_, _ = h.Write([]byte(key))
	return int(h.Sum64() % uint64(c.w))
}

// Add counts key once and returns its new estimate.
func (c *CMS) Add(key string) uint32 {
	c.N++
	est := uint32(math.MaxUint32)
	for i := 0; i < c.d; i++ {
		j := c.idx(key, i)
		c.rows[i][j]++
		est = min(est, c.rows[i][j])
	}
	return est
}

func (c *CMS) Estimate(key string) uint32 {
	est := uint32(math.MaxUint32)
	for i := 0; i < c.d; i++ {
		est = min(est, c.rows[i][c.idx(key, i)])
	}
	return est
}

type Item struct {
	Key   string
	Count uint32
}

type minHeap []Item

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return h[i].Count < h[j].Count }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(Item)) }
func (h *minHeap) Pop() any {
	old := *h
	it := old[len(old)-1]
	*h = old[:len(old)-1]
	return it
}

// TopK tracks the K most frequent keys using a CMS for counts.
type TopK struct {
	K    int
	CMS  *CMS
	heap minHeap
	pos  map[string]int // key → index in heap (linear refresh; K is small)
}

func NewTopK(k int, eps, delta float64) *TopK {
	return &TopK{K: k, CMS: NewCMS(eps, delta), pos: map[string]int{}}
}

func (t *TopK) Add(key string) {
	est := t.CMS.Add(key)
	if _, ok := t.pos[key]; ok {
		for i := range t.heap {
			if t.heap[i].Key == key {
				t.heap[i].Count = est
				heap.Fix(&t.heap, i)
				break
			}
		}
		return
	}
	if len(t.heap) < t.K {
		heap.Push(&t.heap, Item{key, est})
		t.pos[key] = 0
		return
	}
	if est > t.heap[0].Count {
		delete(t.pos, t.heap[0].Key)
		t.heap[0] = Item{key, est}
		t.pos[key] = 0
		heap.Fix(&t.heap, 0)
	}
}

// Top returns the tracked items, most frequent first.
func (t *TopK) Top() []Item {
	out := append([]Item(nil), t.heap...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	return out
}
