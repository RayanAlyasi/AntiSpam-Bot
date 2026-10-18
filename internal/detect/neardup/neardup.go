package neardup

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strings"
	"sync"
	"unicode"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize"
)

// Fingerprint is the SimHash and MinHash signature of one normalized text. An empty MinHash
// means the text had no tokens; such a fingerprint never matches.
type Fingerprint struct {
	SimHash uint64
	MinHash []uint64
	// Tokens is how many distinct tokens the text had; 0 means unknown. A confirmed entry with
	// fewer than MinCertainTokens tokens can only match loosely: "thank you all" must never make
	// every "thank you all!!" known spam.
	Tokens int
}

// MinCertainTokens is the fewest distinct tokens a corpus entry needs before a near-copy of it is
// CERTAIN evidence.
const MinCertainTokens = 4

// Empty reports whether fp has no tokens behind it, so it can't match anything.
func (fp Fingerprint) Empty() bool { return len(fp.MinHash) == 0 }

// minHashSize is the number of MinHash permutations: enough that the Jaccard estimate is within
// about ±0.03 at the 0.9 CERTAIN limit, so a one-word edit of a template doesn't fall below it.
const minHashSize = 256

// Compute fingerprints normalized text. It's deterministic: the same text always gives the
// same fingerprint, across processes and versions of this package with the same seeds.
//
// Tokens are maximal runs of letters, digits and marks, lower-cased. MinHash is over the set of
// tokens (Jaccard of the word sets). SimHash is over tokens and adjacent token pairs, weighted
// by count, so word order matters a little and a rewrite moves many bits.
func Compute(norm string) Fingerprint {
	tokens := tokenize(norm)
	if len(tokens) == 0 {
		return Fingerprint{}
	}
	mh := make([]uint64, minHashSize)
	for i := range mh {
		mh[i] = math.MaxUint64
	}
	var acc [64]int
	add := func(h uint64) {
		for b := range acc {
			if h&(1<<b) != 0 {
				acc[b]++
			} else {
				acc[b]--
			}
		}
	}
	for i, tok := range tokens {
		h := hashString(tok)
		for j := range mh {
			if v := splitmix(h ^ seeds[j]); v < mh[j] {
				mh[j] = v
			}
		}
		add(h)
		if i > 0 {
			add(hashString(tokens[i-1] + " " + tok))
		}
	}
	var sim uint64
	for b, v := range acc {
		if v > 0 {
			sim |= 1 << b
		}
	}
	distinct := map[string]bool{}
	for _, tok := range tokens {
		distinct[tok] = true
	}
	return Fingerprint{SimHash: sim, MinHash: mh, Tokens: len(distinct)}
}

// Key is the matching key of normalized text: the TR39 confusables skeleton of its lower-cased
// form, lower-cased again, so case and homoglyph swaps (Cyrillic "е" for Latin "e") fingerprint
// like the original. Fingerprints of stored and live texts must both be computed from keys.
func Key(norm string) string { return strings.ToLower(normalize.Skeleton(strings.ToLower(norm))) }

// FingerprintText fingerprints a raw text the way the analyzer sees it: normalized (DESIGN.md
// §7.2), keyed, then computed. Use it for confirmed-spam entries.
func FingerprintText(raw string) Fingerprint { return Compute(Key(normalize.Text(raw).Norm)) }

func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r)
	})
}

// hashString is 64-bit FNV-1a, finished with splitmix for better bit mixing.
func hashString(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return splitmix(h)
}

func splitmix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// seeds are the fixed per-permutation seeds. They must never change: stored fingerprints were
// computed with them.
var seeds = func() [minHashSize]uint64 {
	var s [minHashSize]uint64
	x := uint64(0x5eed_a171_5ba3_0001)
	for i := range s {
		x = splitmix(x)
		s[i] = x
	}
	return s
}()

// Distance is the SimHash Hamming distance between two fingerprints.
func Distance(a, b Fingerprint) int { return bits.OnesCount64(a.SimHash ^ b.SimHash) }

// Jaccard estimates the Jaccard similarity of the two token sets: the share of equal MinHash
// positions. Signatures of different or zero length estimate 0.
func Jaccard(a, b Fingerprint) float64 {
	if len(a.MinHash) == 0 || len(a.MinHash) != len(b.MinHash) {
		return 0
	}
	same := 0
	for i, v := range a.MinHash {
		if v == b.MinHash[i] {
			same++
		}
	}
	return float64(same) / float64(len(a.MinHash))
}

// Thresholds are the CERTAIN and loose closeness limits. A fingerprint is CERTAIN when its
// SimHash is within SimHashMaxDistance bits or its MinHash Jaccard is at least
// MinHashMinJaccard; loose (only a rules signal) within the looser pair.
type Thresholds struct {
	SimHashMaxDistance      int
	MinHashMinJaccard       float64
	LooseSimHashMaxDistance int
	LooseMinHashMinJaccard  float64
}

// DefaultThresholds returns the defaults: the tight CERTAIN limits of DESIGN.md §7.0 and §12.1
// (`detection.certain`: 3 bits, Jaccard 0.9) and loose limits of 12 bits or Jaccard 0.4.
func DefaultThresholds() Thresholds {
	return Thresholds{SimHashMaxDistance: 3, MinHashMinJaccard: 0.9, LooseSimHashMaxDistance: 12, LooseMinHashMinJaccard: 0.4}
}

// Validate reports whether the thresholds are usable: 0 ≤ certain distance ≤ loose distance ≤ 64
// and 0 < loose Jaccard ≤ certain Jaccard ≤ 1, so loose is never stricter than certain.
func (t Thresholds) Validate() error {
	var errs []error
	if t.SimHashMaxDistance < 0 || t.SimHashMaxDistance > t.LooseSimHashMaxDistance || t.LooseSimHashMaxDistance > 64 {
		errs = append(errs, fmt.Errorf("simhash distances %d (certain) and %d (loose): want 0 ≤ certain ≤ loose ≤ 64",
			t.SimHashMaxDistance, t.LooseSimHashMaxDistance))
	}
	if !(t.LooseMinHashMinJaccard > 0 && t.LooseMinHashMinJaccard <= t.MinHashMinJaccard && t.MinHashMinJaccard <= 1) {
		errs = append(errs, fmt.Errorf("minhash jaccard %v (certain) and %v (loose): want 0 < loose ≤ certain ≤ 1",
			t.MinHashMinJaccard, t.LooseMinHashMinJaccard))
	}
	return errors.Join(errs...)
}

// Closeness is how close a fingerprint is to the corpus.
type Closeness int

// Closeness levels.
const (
	None    Closeness = iota // not near any entry
	Loose                    // related: a rules signal that sends the message to the AI
	Certain                  // a near-copy: CERTAIN known evidence
)

func (c Closeness) String() string {
	switch c {
	case Loose:
		return "loose"
	case Certain:
		return "certain"
	default:
		return "none"
	}
}

// Match is the result of matching one fingerprint against the corpus. Allowed is set when an
// allow-listed (overturned) fingerprint suppressed the match; Closeness is then None.
type Match struct {
	Closeness Closeness
	EntryID   string
	Distance  int
	Jaccard   float64
	Allowed   bool
}

// Index holds confirmed-spam fingerprints and allow-listed fingerprints for one scope. It's safe
// for concurrent use. Lookups scan every entry, which stays well inside the T1 budget for the
// corpus sizes of v0.1 (thousands of entries); an LSH front end can replace the scan later
// without changing results.
type Index struct {
	th      Thresholds
	mu      sync.RWMutex
	entries map[string]Fingerprint
	allowed []Fingerprint
}

// NewIndex returns an empty index. Invalid thresholds fall back to the defaults, so a bad
// operator value can never loosen CERTAIN matching beyond its documented range.
func NewIndex(t Thresholds) *Index {
	if t.Validate() != nil {
		t = DefaultThresholds()
	}
	return &Index{th: t, entries: map[string]Fingerprint{}}
}

// Thresholds returns the limits the index applies.
func (ix *Index) Thresholds() Thresholds { return ix.th }

// Add adds or replaces a confirmed-spam entry. An empty fingerprint is ignored: it could never match.
func (ix *Index) Add(id string, fp Fingerprint) {
	if fp.Empty() {
		return
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.entries[id] = clone(fp)
}

// Remove retracts an entry; it stops matching at once.
func (ix *Index) Remove(id string) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	delete(ix.entries, id)
}

// Len returns the number of confirmed entries.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.entries)
}

// Allow adds an overturned fingerprint to the allow-list: anything within the CERTAIN limits of
// it never matches again (DESIGN.md §7.5 "Overturns remove what was learned").
func (ix *Index) Allow(fp Fingerprint) {
	if fp.Empty() {
		return
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.allowed = append(ix.allowed, clone(fp))
}

// Match finds the closest confirmed entry, after the allow-list check. Certain beats loose; among
// equals the higher Jaccard, then the smaller distance, then the smaller ID wins, so the result
// never depends on map order.
func (ix *Index) Match(fp Fingerprint) Match {
	if fp.Empty() {
		return Match{}
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	for _, a := range ix.allowed {
		if ix.closeness(Distance(fp, a), Jaccard(fp, a)) == Certain {
			return Match{Allowed: true}
		}
	}
	var best Match
	for id, e := range ix.entries {
		d, j := Distance(fp, e), Jaccard(fp, e)
		c := ix.closeness(d, j)
		if c == None {
			continue
		}
		if c == Certain && e.Tokens > 0 && e.Tokens < MinCertainTokens {
			c = Loose
		}
		m := Match{Closeness: c, EntryID: id, Distance: d, Jaccard: j}
		if better(m, best) {
			best = m
		}
	}
	return best
}

// Allowed reports whether fp is within the CERTAIN limits of an allow-listed fingerprint.
// Callers matching against several indexes check every index's allow-list first, so an overturn
// recorded for a group also suppresses matches against shared (global) entries.
func (ix *Index) Allowed(fp Fingerprint) bool {
	if fp.Empty() {
		return false
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	for _, a := range ix.allowed {
		if ix.closeness(Distance(fp, a), Jaccard(fp, a)) == Certain {
			return true
		}
	}
	return false
}

func (ix *Index) closeness(d int, j float64) Closeness {
	switch {
	case d <= ix.th.SimHashMaxDistance || j >= ix.th.MinHashMinJaccard:
		return Certain
	case d <= ix.th.LooseSimHashMaxDistance || j >= ix.th.LooseMinHashMinJaccard:
		return Loose
	default:
		return None
	}
}

func better(a, b Match) bool {
	switch {
	case a.Closeness != b.Closeness:
		return a.Closeness > b.Closeness
	case a.Jaccard != b.Jaccard:
		return a.Jaccard > b.Jaccard
	case a.Distance != b.Distance:
		return a.Distance < b.Distance
	default:
		return a.EntryID < b.EntryID
	}
}

func clone(fp Fingerprint) Fingerprint {
	return Fingerprint{SimHash: fp.SimHash, MinHash: append([]uint64(nil), fp.MinHash...), Tokens: fp.Tokens}
}

// MinHashBytes encodes the MinHash signature for storage: 8 bytes per value, little-endian.
func (fp Fingerprint) MinHashBytes() []byte {
	b := make([]byte, 0, 8*len(fp.MinHash))
	for _, v := range fp.MinHash {
		b = binary.LittleEndian.AppendUint64(b, v)
	}
	return b
}

// MinHashFromBytes decodes a signature MinHashBytes encoded.
func MinHashFromBytes(b []byte) ([]uint64, error) {
	if len(b)%8 != 0 {
		return nil, fmt.Errorf("neardup: minhash of %d bytes isn't a multiple of 8", len(b))
	}
	out := make([]uint64, len(b)/8)
	for i := range out {
		out[i] = binary.LittleEndian.Uint64(b[8*i:])
	}
	return out, nil
}
