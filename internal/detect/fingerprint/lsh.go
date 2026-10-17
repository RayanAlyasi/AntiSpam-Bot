package fingerprint

import (
	"cmp"
	"fmt"
	"slices"
	"sync"
)

// SimMatch is one SimHash index hit.
type SimMatch[ID cmp.Ordered] struct {
	ID       ID
	Distance int
}

// SimIndex finds stored SimHashes within a Hamming distance of a query. It splits the 64 bits
// into maxDist+1 bands: two hashes within maxDist bits agree exactly on at least one band
// (pigeonhole), so a lookup per band finds every match, and each candidate is then checked
// exactly. It is safe for concurrent use.
type SimIndex[ID cmp.Ordered] struct {
	maxDist int
	bands   []bandRange
	mu      sync.RWMutex
	hashes  map[ID]SimHash
	buckets []map[uint64][]ID
}

type bandRange struct{ shift, width uint }

// MaxSimDistance is the largest supported index distance; beyond it bands become too narrow
// to narrow anything down.
const MaxSimDistance = 15

// NewSimIndex returns an index answering queries up to maxDist bits (0–MaxSimDistance).
func NewSimIndex[ID cmp.Ordered](maxDist int) (*SimIndex[ID], error) {
	if maxDist < 0 || maxDist > MaxSimDistance {
		return nil, fmt.Errorf("simhash index distance %d: want 0–%d", maxDist, MaxSimDistance)
	}
	n := maxDist + 1
	bands := make([]bandRange, n)
	shift := uint(0)
	for i := range n {
		w := uint(64 / n)
		if i < 64%n {
			w++
		}
		bands[i] = bandRange{shift: shift, width: w}
		shift += w
	}
	ix := &SimIndex[ID]{maxDist: maxDist, bands: bands, hashes: map[ID]SimHash{}, buckets: make([]map[uint64][]ID, n)}
	for i := range ix.buckets {
		ix.buckets[i] = map[uint64][]ID{}
	}
	return ix, nil
}

func (b bandRange) key(h SimHash) uint64 {
	return (uint64(h) >> b.shift) & (1<<b.width - 1)
}

// Add stores h under id, replacing any earlier hash for id.
func (ix *SimIndex[ID]) Add(id ID, h SimHash) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if old, ok := ix.hashes[id]; ok {
		ix.removeLocked(id, old)
	}
	ix.hashes[id] = h
	for i, b := range ix.bands {
		k := b.key(h)
		ix.buckets[i][k] = append(ix.buckets[i][k], id)
	}
}

// Remove deletes id; removing an unknown id does nothing.
func (ix *SimIndex[ID]) Remove(id ID) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if old, ok := ix.hashes[id]; ok {
		ix.removeLocked(id, old)
	}
}

func (ix *SimIndex[ID]) removeLocked(id ID, h SimHash) {
	delete(ix.hashes, id)
	for i, b := range ix.bands {
		k := b.key(h)
		ids := slices.DeleteFunc(ix.buckets[i][k], func(x ID) bool { return x == id })
		if len(ids) == 0 {
			delete(ix.buckets[i], k)
		} else {
			ix.buckets[i][k] = ids
		}
	}
}

// Len returns the number of stored hashes.
func (ix *SimIndex[ID]) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.hashes)
}

// Near returns every stored hash within maxDist bits of h, nearest first (ties by ID).
// maxDist above the index's own distance is capped to it.
func (ix *SimIndex[ID]) Near(h SimHash, maxDist int) []SimMatch[ID] {
	if maxDist < 0 {
		return nil
	}
	maxDist = min(maxDist, ix.maxDist)
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	seen := map[ID]bool{}
	var out []SimMatch[ID]
	for i, b := range ix.bands {
		for _, id := range ix.buckets[i][b.key(h)] {
			if seen[id] {
				continue
			}
			seen[id] = true
			if d := Hamming(h, ix.hashes[id]); d <= maxDist {
				out = append(out, SimMatch[ID]{ID: id, Distance: d})
			}
		}
	}
	slices.SortFunc(out, func(a, b SimMatch[ID]) int {
		return cmp.Or(cmp.Compare(a.Distance, b.Distance), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// MinMatch is one MinHash index hit.
type MinMatch[ID cmp.Ordered] struct {
	ID      ID
	Jaccard float64
}

// MinHash LSH banding: 32 bands of 4 rows. A pair with Jaccard 0.9 is a candidate with
// probability ≈ 1, 0.5 with ≈ 0.87; candidates are then checked against the threshold.
const (
	minBands = 32
	minRows  = MinHashSize / minBands
)

// MinIndex finds stored MinHash signatures whose estimated Jaccard similarity to a query is
// at least a threshold. It is safe for concurrent use.
type MinIndex[ID cmp.Ordered] struct {
	mu      sync.RWMutex
	sigs    map[ID]*MinHash
	buckets [minBands]map[uint64][]ID
}

// NewMinIndex returns an empty index.
func NewMinIndex[ID cmp.Ordered]() *MinIndex[ID] {
	ix := &MinIndex[ID]{sigs: map[ID]*MinHash{}}
	for i := range ix.buckets {
		ix.buckets[i] = map[uint64][]ID{}
	}
	return ix
}

func bandKey(m *MinHash, band int) uint64 {
	h := uint64(band) + 0x9e3779b97f4a7c15
	for _, v := range m[band*minRows : (band+1)*minRows] {
		h = mix64(h ^ uint64(v))
	}
	return h
}

// Add stores sig under id, replacing any earlier signature for id.
func (ix *MinIndex[ID]) Add(id ID, sig MinHash) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if old, ok := ix.sigs[id]; ok {
		ix.removeLocked(id, old)
	}
	ix.sigs[id] = &sig
	for b := range minBands {
		k := bandKey(&sig, b)
		ix.buckets[b][k] = append(ix.buckets[b][k], id)
	}
}

// Remove deletes id; removing an unknown id does nothing.
func (ix *MinIndex[ID]) Remove(id ID) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if old, ok := ix.sigs[id]; ok {
		ix.removeLocked(id, old)
	}
}

func (ix *MinIndex[ID]) removeLocked(id ID, sig *MinHash) {
	delete(ix.sigs, id)
	for b := range minBands {
		k := bandKey(sig, b)
		ids := slices.DeleteFunc(ix.buckets[b][k], func(x ID) bool { return x == id })
		if len(ids) == 0 {
			delete(ix.buckets[b], k)
		} else {
			ix.buckets[b][k] = ids
		}
	}
}

// Len returns the number of stored signatures.
func (ix *MinIndex[ID]) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.sigs)
}

// Similar returns the stored signatures with estimated Jaccard ≥ minJaccard that share at
// least one LSH band with sig, most similar first (ties by ID). Pairs well below 0.5 are
// usually not candidates at all, so thresholds under 0.5 are best effort.
func (ix *MinIndex[ID]) Similar(sig *MinHash, minJaccard float64) []MinMatch[ID] {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	seen := map[ID]bool{}
	var out []MinMatch[ID]
	for b := range minBands {
		for _, id := range ix.buckets[b][bandKey(sig, b)] {
			if seen[id] {
				continue
			}
			seen[id] = true
			if j := Jaccard(sig, ix.sigs[id]); j >= minJaccard {
				out = append(out, MinMatch[ID]{ID: id, Jaccard: j})
			}
		}
	}
	slices.SortFunc(out, func(a, b MinMatch[ID]) int {
		return cmp.Or(cmp.Compare(b.Jaccard, a.Jaccard), cmp.Compare(a.ID, b.ID))
	})
	return out
}
