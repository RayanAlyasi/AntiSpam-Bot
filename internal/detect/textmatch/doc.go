// Package textmatch holds the text matchers that rules run on normalized text (DESIGN.md
// §7.2, "Matching on Norm"): an Aho-Corasick automaton for keyword and phrase lists, RE2
// regular expressions with length and complexity limits for admin-supplied patterns, and
// fuzzy matching (Damerau-Levenshtein and token-set similarity) for near-misses of short
// phrases. Everything here is pure and safe for concurrent use once built.
package textmatch
