// Package fingerprint computes near-duplicate fingerprints of normalized text (DESIGN.md §7.2,
// §7.5): a 64-bit SimHash and a 128-value MinHash signature over character shingles, plus
// in-memory LSH indexes that find corpus entries within a Hamming distance or above a Jaccard
// similarity without comparing against every entry.
//
// The functions are pure and deterministic across processes and releases: fingerprints are
// stored in corpus_entries and message_index, so changing the shingling or hashing invalidates
// every stored fingerprint and needs a migration that recomputes them.
package fingerprint
