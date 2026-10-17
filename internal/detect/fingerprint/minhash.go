package fingerprint

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// MinHashSize is the number of hash functions in a MinHash signature.
const MinHashSize = 128

// MinHash is a MinHash signature of the set of a text's shingles. The fraction of equal
// positions between two signatures estimates the Jaccard similarity of the shingle sets.
type MinHash [MinHashSize]uint32

// seeds are the fixed per-position seeds; they are derived once from a constant so stored
// signatures stay comparable across processes.
var seeds = func() [MinHashSize]uint64 {
	var s [MinHashSize]uint64
	x := uint64(0x9e3779b97f4a7c15)
	for i := range s {
		x += 0x9e3779b97f4a7c15
		s[i] = mix64(x)
	}
	return s
}()

// ComputeMinHash returns the MinHash of text's distinct shingles. Empty text gives ok false.
func ComputeMinHash(text string) (m MinHash, ok bool) {
	sh := Shingles(text)
	if len(sh) == 0 {
		return MinHash{}, false
	}
	for i := range m {
		m[i] = math.MaxUint32
	}
	for _, s := range sh {
		for i := range m {
			if v := uint32(mix64(s^seeds[i]) >> 32); v < m[i] {
				m[i] = v
			}
		}
	}
	return m, true
}

// Jaccard estimates the Jaccard similarity of the two shingle sets (0–1).
func Jaccard(a, b *MinHash) float64 {
	eq := 0
	for i := range a {
		if a[i] == b[i] {
			eq++
		}
	}
	return float64(eq) / MinHashSize
}

// MinHashBytes is the length of a MinHash's binary encoding.
const MinHashBytes = MinHashSize * 4

// ErrMinHashLength is returned by ParseMinHash for input of the wrong length.
var ErrMinHashLength = errors.New("fingerprint: minhash has the wrong length")

// Bytes encodes the signature as MinHashBytes little-endian bytes (the corpus_entries.minhash
// column).
func (m *MinHash) Bytes() []byte {
	out := make([]byte, MinHashBytes)
	for i, v := range m {
		binary.LittleEndian.PutUint32(out[i*4:], v)
	}
	return out
}

// ParseMinHash decodes Bytes.
func ParseMinHash(b []byte) (MinHash, error) {
	var m MinHash
	if len(b) != MinHashBytes {
		return m, fmt.Errorf("parse minhash of %d bytes: %w", len(b), ErrMinHashLength)
	}
	for i := range m {
		m[i] = binary.LittleEndian.Uint32(b[i*4:])
	}
	return m, nil
}
