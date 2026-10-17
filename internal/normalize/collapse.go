package normalize

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// minCollapseRun is the number of spelled-out single letters that collapse
// into a word. Runs of two (a.m, "a b") are left alone.
const minCollapseRun = 3

// maxCollapseGap is the longest gap, in runes, between spelled-out letters.
const maxCollapseGap = 3

// CollapseSeparators joins words spelled letter by letter (DESIGN.md §7.2
// step 6): f.r.e.e, f r e e and f-r-e-e become free. A run collapses only
// when it has minCollapseRun or more single letters with short gaps of
// spaces and separators between them, and no separator ties it to a longer
// word (a.b.c.com, J. R. R. Tolkien). Digits never collapse.
func CollapseSeparators(s string) string {
	return collapse(s, unicode.IsLetter, nil)
}

// SqueezeRepeats shortens runs of three or more identical letters to two
// (freeeee → free, ههههه → هه). Double letters and digits are unchanged.
func SqueezeRepeats(s string) string {
	var b strings.Builder
	var prev rune
	run := 0
	changed := false
	for i, r := range s {
		if unicode.IsLetter(r) && run > 0 && unicode.ToLower(r) == unicode.ToLower(prev) {
			run++
		} else {
			prev, run = r, 1
		}
		if run > 2 {
			if !changed {
				b.Grow(len(s))
				b.WriteString(s[:i])
				changed = true
			}
			continue
		}
		if changed {
			b.WriteRune(r)
		}
	}
	if !changed {
		return s
	}
	return b.String()
}

func isCollapseSeparator(r rune) bool {
	switch r {
	case '.', '-', '_', '*', '·', '•', ',', '|', '/', '+', '~', '\t':
		return true
	}
	return unicode.Is(unicode.Zs, r)
}

type tokenKind uint8

const (
	tokWord  tokenKind = iota // unit characters and their marks
	tokGap                    // collapse separators
	tokOther                  // anything else, digits included
)

type token struct {
	kind  tokenKind
	text  string
	units int  // unit characters in a word
	plain bool // a gap made only of spaces
}

// collapse joins runs of single-unit words. unit says which characters make
// up words; accept, when set, must approve a run before it collapses.
func collapse(s string, unit func(rune) bool, accept func(run []token) bool) string {
	toks := tokenize(s, unit)
	var b strings.Builder
	changed := false
	for i := 0; i < len(toks); {
		end := runEnd(toks, i)
		if end == i || !collapsible(toks, i, end, accept) {
			// A rejected run is left whole; none of its tails is tried again.
			for _, t := range toks[i : end+1] {
				if changed {
					b.WriteString(t.text)
				}
			}
			i = end + 1
			continue
		}
		if !changed {
			b.Grow(len(s))
			for _, t := range toks[:i] {
				b.WriteString(t.text)
			}
			changed = true
		}
		for j := i; j <= end; j += 2 {
			b.WriteString(toks[j].text)
		}
		i = end + 1
	}
	if !changed {
		return s
	}
	return b.String()
}

// runEnd returns the index of the last single-unit word in the run of
// single-unit words starting at toks[i], or i when there is no run of
// minCollapseRun.
func runEnd(toks []token, i int) int {
	if !isSingle(toks, i) {
		return i
	}
	end, n := i, 1
	for end+2 < len(toks) && isShortGap(toks, end+1) && isSingle(toks, end+2) {
		end += 2
		n++
	}
	if n < minCollapseRun {
		return i
	}
	return end
}

// collapsible rejects a run that a separator ties to a neighbouring word.
func collapsible(toks []token, start, end int, accept func([]token) bool) bool {
	if start > 0 && tied(toks, start-1, start-2) {
		return false
	}
	if end+1 < len(toks) && tied(toks, end+1, end+2) {
		return false
	}
	return accept == nil || accept(toks[start:end+1])
}

// tied reports whether the token next to a run joins it to another word:
// the run touches a non-word token directly, or a gap with a separator in it
// leads to a word or number.
func tied(toks []token, near, far int) bool {
	t := toks[near]
	switch t.kind {
	case tokOther:
		return startsAlnum(t.text, near < far)
	case tokGap:
		if t.plain || far < 0 || far >= len(toks) {
			return false
		}
		f := toks[far]
		return f.kind == tokWord || f.kind == tokOther && startsAlnum(f.text, near < far)
	case tokWord:
		// Words merge with adjacent words, so a run never touches one; treat it
		// as tied to stay safe.
	}
	return true
}

// startsAlnum reports whether s begins (or, when fromStart is false, ends)
// with a letter or digit.
func startsAlnum(s string, fromStart bool) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	if fromStart {
		r, _ = utf8.DecodeRuneInString(s)
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isSingle(toks []token, i int) bool {
	return i < len(toks) && toks[i].kind == tokWord && toks[i].units == 1
}

func isShortGap(toks []token, i int) bool {
	return toks[i].kind == tokGap && utf8.RuneCountInString(toks[i].text) <= maxCollapseGap
}

// tokenize splits s into words, gaps and other text. Marks stay with the
// word they follow.
func tokenize(s string, unit func(rune) bool) []token {
	var toks []token
	start := 0
	kind := tokOther
	units := 0
	plain := true
	flush := func(end int) {
		if end > start {
			toks = append(toks, token{kind: kind, text: s[start:end], units: units, plain: plain})
		}
		start, units, plain = end, 0, true
	}
	for i, r := range s {
		var k tokenKind
		switch {
		case unit(r):
			k = tokWord
		case unicode.IsMark(r) && kind == tokWord && i > start:
			k = tokWord
		case isCollapseSeparator(r):
			k = tokGap
		default:
			k = tokOther
		}
		if i == start {
			kind = k
		} else if k != kind || k == tokOther && kind != tokOther {
			flush(i)
			kind = k
		}
		if k == tokWord && unit(r) {
			units++
		}
		if k == tokGap && !unicode.Is(unicode.Zs, r) {
			plain = false
		}
	}
	flush(len(s))
	return toks
}
