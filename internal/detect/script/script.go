// Package script detects language and script anomalies in text (DESIGN.md §6.1, §7.2): words
// that mix look-alike scripts (Latin "frее" spelled with Cyrillic "е") and text padded with
// invisible characters. Both are obfuscation, so they are weighted signals; ordinary bilingual
// text, such as Arabic and English in one message, is not an anomaly.
package script

import (
	"strings"
	"unicode"
)

// Script is a writing system that look-alike spoofing mixes into Latin words.
type Script string

// The scripts MixedWords tracks. Others (Arabic, Hebrew, Han, Devanagari, …) don't have
// Latin look-alikes in practice, and mixing them with Latin inside a word happens in
// ordinary typing (a missing space), so they are ignored.
const (
	Latin    Script = "Latin"
	Cyrillic Script = "Cyrillic"
	Greek    Script = "Greek"
	Armenian Script = "Armenian"
	Cherokee Script = "Cherokee"
)

var tracked = []struct {
	name  Script
	table *unicode.RangeTable
}{
	{Latin, unicode.Latin},
	{Cyrillic, unicode.Cyrillic},
	{Greek, unicode.Greek},
	{Armenian, unicode.Armenian},
	{Cherokee, unicode.Cherokee},
}

// MinWordLetters is the fewest letters a word needs before its mix counts: one- and
// two-letter tokens are too often units, symbols or abbreviations (Ωm, μg).
const MinWordLetters = 3

// MixedWord is one word whose letters come from two or more tracked scripts.
type MixedWord struct {
	Word    string
	Scripts []Script // in tracked order
}

// MixedWords returns the words of text (runs of letters, marks and digits) whose letters mix
// tracked scripts, in order of appearance. It must run on text that has had NFKC applied but
// no confusables skeleton, since the skeleton erases exactly this evidence.
func MixedWords(text string) []MixedWord {
	var out []MixedWord
	for _, w := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsMark(r) && !unicode.IsDigit(r)
	}) {
		var seen [5]bool
		letters := 0
		for _, r := range w {
			if !unicode.IsLetter(r) {
				continue
			}
			letters++
			for i, t := range tracked {
				if unicode.Is(t.table, r) {
					seen[i] = true
					break
				}
			}
		}
		var scripts []Script
		for i, s := range seen {
			if s {
				scripts = append(scripts, tracked[i].name)
			}
		}
		if letters >= MinWordLetters && len(scripts) >= 2 {
			out = append(out, MixedWord{Word: w, Scripts: scripts})
		}
	}
	return out
}
