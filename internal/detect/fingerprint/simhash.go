package fingerprint

import "math/bits"

// SimHash is a 64-bit Charikar SimHash of normalized text. Similar texts have SimHashes that
// differ in few bits; Hamming gives the distance.
type SimHash uint64

// ComputeSimHash returns the SimHash of text's shingles, each occurrence weighted 1. Empty
// text (no letters or digits) gives 0 and ok false: it has no fingerprint and must not be
// compared or stored.
func ComputeSimHash(text string) (h SimHash, ok bool) {
	sh := Shingles(text)
	if len(sh) == 0 {
		return 0, false
	}
	var acc [64]int
	for _, s := range sh {
		for i := range 64 {
			if s&(1<<i) != 0 {
				acc[i]++
			} else {
				acc[i]--
			}
		}
	}
	var out uint64
	for i, v := range acc {
		if v > 0 {
			out |= 1 << i
		}
	}
	return SimHash(out), true
}

// Hamming returns the number of differing bits between a and b (0–64).
func Hamming(a, b SimHash) int {
	return bits.OnesCount64(uint64(a ^ b))
}

// Int64 returns the SimHash as the signed value stored in a Postgres bigint column.
func (h SimHash) Int64() int64 { return int64(h) }

// SimHashFromInt64 reverses Int64.
func SimHashFromInt64(v int64) SimHash { return SimHash(v) }
