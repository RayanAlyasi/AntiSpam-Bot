// Package neardup is the T1 near-duplicate matcher against the confirmed-spam corpus
// (DESIGN.md §7.0, §7.1, §7.5): SimHash and MinHash fingerprints of normalized text, an
// allow-list of overturned fingerprints checked first, and tight CERTAIN thresholds.
package neardup

// Stub for F-117: declares only the exported names the acceptance tests use
// (.agent/contracts/F-088.md, section F-117). The builder replaces this file.

// Fingerprint is the SimHash and MinHash signature of one normalized text. An empty MinHash
// means the text had no tokens; such a fingerprint never matches.
type Fingerprint struct {
	SimHash uint64
	MinHash []uint64
}

// Compute fingerprints normalized text.
func Compute(norm string) Fingerprint { panic("not implemented") }

// Thresholds are the CERTAIN and loose closeness limits.
type Thresholds struct {
	SimHashMaxDistance      int
	MinHashMinJaccard       float64
	LooseSimHashMaxDistance int
	LooseMinHashMinJaccard  float64
}

// DefaultThresholds returns the defaults (DESIGN.md §12.1 detection.certain).
func DefaultThresholds() Thresholds { panic("not implemented") }

// Validate reports whether the thresholds are usable.
func (t Thresholds) Validate() error { panic("not implemented") }

// Closeness is how close a fingerprint is to the corpus.
type Closeness int

// Closeness levels.
const (
	None Closeness = iota
	Loose
	Certain
)

// Match is the result of matching one fingerprint against the corpus.
type Match struct {
	Closeness Closeness
	EntryID   string
	Distance  int
	Jaccard   float64
	Allowed   bool
}

// Index holds confirmed-spam fingerprints and allow-listed fingerprints for one scope.
type Index struct{}

// NewIndex returns an empty index.
func NewIndex(t Thresholds) *Index { panic("not implemented") }

// Add adds or replaces a confirmed-spam entry.
func (ix *Index) Add(id string, fp Fingerprint) { panic("not implemented") }

// Remove retracts an entry.
func (ix *Index) Remove(id string) { panic("not implemented") }

// Allow adds an overturned fingerprint to the allow-list.
func (ix *Index) Allow(fp Fingerprint) { panic("not implemented") }

// Match finds the closest confirmed entry, after the allow-list check.
func (ix *Index) Match(fp Fingerprint) Match { panic("not implemented") }
