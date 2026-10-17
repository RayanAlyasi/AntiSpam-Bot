package fingerprint

import (
	"strings"
	"unicode"
)

// ShingleSize is the length, in runes, of the character shingles both fingerprints use.
// Character shingles (rather than words) keep short messages fingerprintable and make a
// one-letter edit change only a few shingles.
const ShingleSize = 4

// Canonical prepares normalized text for shingling: lower case, every run of characters that
// are not letters, marks or digits becomes one space, and leading and trailing spaces go.
// Punctuation, emoji and spacing tricks therefore don't change a fingerprint.
func Canonical(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	space := false
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		space = true
	}
	return b.String()
}

// Shingles returns the hashes of the overlapping ShingleSize-rune shingles of Canonical(text),
// in order, with repeats. Text shorter than one shingle gives a single shingle of the whole
// text; empty text gives none.
func Shingles(text string) []uint64 {
	runes := []rune(Canonical(text))
	if len(runes) == 0 {
		return nil
	}
	if len(runes) <= ShingleSize {
		return []uint64{hashRunes(runes)}
	}
	out := make([]uint64, 0, len(runes)-ShingleSize+1)
	for i := 0; i+ShingleSize <= len(runes); i++ {
		out = append(out, hashRunes(runes[i:i+ShingleSize]))
	}
	return out
}

// hashRunes is FNV-1a over the runes' code points followed by a finalizer, so every output
// bit depends on every input rune (SimHash needs well-mixed bits).
func hashRunes(rs []rune) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for _, r := range rs {
		v := uint32(r)
		for range 4 {
			h ^= uint64(v & 0xff)
			h *= prime
			v >>= 8
		}
	}
	return mix64(h)
}

// mix64 is the SplitMix64 finalizer.
func mix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}
