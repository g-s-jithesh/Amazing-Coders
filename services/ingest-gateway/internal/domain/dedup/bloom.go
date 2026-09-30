// Package dedup is the gateway's first-line duplicate filter: a rotating pair of Bloom filters over
// (vin, seq) covering a sliding window. A miss means "definitely new"; a hit means "maybe seen" and
// must be confirmed by an exact store (Redis) before dropping, so a false positive never loses data.
//
// Sizing: m = −n·ln p / (ln 2)², k = (m/n)·ln 2 for n keys per half-window at false-positive rate p.
// Insert/Test are O(k); not safe for concurrent use (callers serialise).
package dedup

import (
	"hash/fnv"
	"math"
)

type bloom struct {
	bits []uint64
	m    uint64
}

func newBloom(m uint64) *bloom { return &bloom{bits: make([]uint64, (m+63)/64), m: m} }

func (b *bloom) set(i uint64)      { b.bits[i/64] |= 1 << (i % 64) }
func (b *bloom) get(i uint64) bool { return b.bits[i/64]&(1<<(i%64)) != 0 }

// Rotating holds the current and previous half-window filters.
type Rotating struct {
	cur, prev *bloom
	m         uint64
	k         int
	halfMs    int64
	rotatedMs int64
}

// NewRotating sizes the filters for keysPerHalfWindow keys at fpRate, rotating every windowMs/2.
func NewRotating(keysPerHalfWindow int, fpRate float64, windowMs, nowMs int64) *Rotating {
	n := math.Max(1, float64(keysPerHalfWindow))
	m := uint64(math.Ceil(-n * math.Log(fpRate) / (math.Ln2 * math.Ln2)))
	k := int(math.Max(1, math.Round(float64(m)/n*math.Ln2)))
	return &Rotating{cur: newBloom(m), prev: newBloom(m), m: m, k: k, halfMs: windowMs / 2, rotatedMs: nowMs}
}

// K and M expose the sizing (for metrics and tests).
func (r *Rotating) K() int    { return r.k }
func (r *Rotating) M() uint64 { return r.m }

func (r *Rotating) rotate(nowMs int64) {
	for nowMs-r.rotatedMs >= r.halfMs {
		r.prev, r.cur = r.cur, r.prev
		clear(r.cur.bits)
		r.rotatedMs += r.halfMs
	}
}

// hashes uses Kirsch–Mitzenmacher double hashing, h_i = h1 + i·h2, with h1/h2 the two halves of
// a 128-bit FNV-1a hash.
func hashes(vin string, seq uint64) (uint64, uint64) {
	h := fnv.New128a()
	_, _ = h.Write([]byte(vin))
	var b [8]byte
	for i := range b {
		b[i] = byte(seq >> (8 * i))
	}
	_, _ = h.Write(b[:])
	var sum [16]byte
	h.Sum(sum[:0])
	var h1, h2 uint64
	for i := 0; i < 8; i++ {
		h1 = h1<<8 | uint64(sum[i])
		h2 = h2<<8 | uint64(sum[8+i])
	}
	return h1, h2 | 1
}

// mayContain tests without inserting (used to measure the false-positive rate).
func (r *Rotating) mayContain(vin string, seq uint64) bool {
	h1, h2 := hashes(vin, seq)
	inCur, inPrev := true, true
	for i := 0; i < r.k; i++ {
		idx := (h1 + uint64(i)*h2) % r.m
		inCur = inCur && r.cur.get(idx)
		inPrev = inPrev && r.prev.get(idx)
	}
	return inCur || inPrev
}

// SeenOrAdd reports whether (vin, seq) may have been seen within the window, and records it.
func (r *Rotating) SeenOrAdd(vin string, seq uint64, nowMs int64) bool {
	r.rotate(nowMs)
	h1, h2 := hashes(vin, seq)
	inCur, inPrev := true, true
	for i := 0; i < r.k; i++ {
		idx := (h1 + uint64(i)*h2) % r.m
		inCur = inCur && r.cur.get(idx)
		inPrev = inPrev && r.prev.get(idx)
		r.cur.set(idx)
	}
	return inCur || inPrev
}
