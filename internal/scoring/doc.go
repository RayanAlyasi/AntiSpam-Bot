// Package scoring combines signals into a score, a band (CERTAIN, UNCERTAIN, still
// uncertain) and a confidence source (known, rules, ai), as described in DESIGN.md §7.0
// and §7.3. Trust never changes the score.
package scoring
