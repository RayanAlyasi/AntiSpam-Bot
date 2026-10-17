// Package normalize turns raw text surfaces into the normalized form used for matching
// (DESIGN.md §7.2): NFKC, invisible-character stripping, confusables, leetspeak, Arabic
// folding, separator collapsing and phone-number extraction. The raw text is kept for evidence.
package normalize
