package textmatch

import (
	"strings"
	"unicode/utf8"
)

// Damerau returns the optimal-string-alignment Damerau-Levenshtein distance between a and b,
// in runes (insertions, deletions, substitutions and adjacent transpositions). It stops early
// once the distance is certain to exceed limit and then returns limit+1 and false.
func Damerau(a, b string, limit int) (int, bool) {
	ra, rb := []rune(a), []rune(b)
	if d := len(ra) - len(rb); d > limit || -d > limit {
		return limit + 1, false
	}
	// Three rolling rows: two back (for transpositions), previous, current.
	prev2 := make([]int, len(rb)+1)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			v := min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				v = min(v, prev2[j-2]+1)
			}
			cur[j] = v
			rowMin = min(rowMin, v)
		}
		if rowMin > limit {
			return limit + 1, false
		}
		prev2, prev, cur = prev, cur, prev2
	}
	d := prev[len(rb)]
	return d, d <= limit
}

// Tokens splits prepared text into words: runs of letters, marks and digits.
func Tokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !isWordRune(r) })
}

// TokenSetSimilarity is the Jaccard similarity of the two token sets (0–1): word order and
// repeated words don't matter. Two empty sets have similarity 0.
func TokenSetSimilarity(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	sa := map[string]bool{}
	for _, t := range a {
		sa[t] = true
	}
	sb := map[string]bool{}
	inter := 0
	for _, t := range b {
		if sb[t] {
			continue
		}
		sb[t] = true
		if sa[t] {
			inter++
		}
	}
	return float64(inter) / float64(len(sa)+len(sb)-inter)
}

// Fuzzy matching limits. Phrases shorter than MinFuzzyRunes are never fuzzy-matched: at that
// length one edit turns most words into other real words.
const (
	MinFuzzyRunes = 6
	// MinTokenSetTokens is the fewest phrase words for which a reordered match counts.
	MinTokenSetTokens = 3
	// MinTokenSetSimilarity is the token-set similarity a reordered window needs.
	MinTokenSetSimilarity = 0.8
)

// MaxEdits is the number of edits a fuzzy match of a phrase of n runes may contain:
// none below MinFuzzyRunes, 1 up to 10 runes, 2 up to 20, 3 beyond.
func MaxEdits(n int) int {
	switch {
	case n < MinFuzzyRunes:
		return 0
	case n <= 10:
		return 1
	case n <= 20:
		return 2
	default:
		return 3
	}
}

// FuzzyHit is a near-miss of a phrase: the matched words of the text and how similar they
// are (1 − edits/length for edit matches, the token-set similarity for reordered ones).
type FuzzyHit struct {
	Matched    string
	Similarity float64
	Edits      int // -1 for a token-set (reordered) match
}

// MaxFuzzyTokens bounds the words of one text that fuzzy matching looks at, so a padded
// message can't make the T1 analyzer blow its budget: cost grows with words × phrases.
const MaxFuzzyTokens = 200

// Windows is a text prepared for fuzzy matching against many phrases: its first
// MaxFuzzyTokens words, with the word windows of each size built once and reused. It is
// not safe for concurrent use.
type Windows struct {
	tokens []string
	bySize map[int][]window
}

type window struct {
	joined string
	runes  int
	start  int
}

// NewWindows prepares text (prepared or in Words form).
func NewWindows(text string) *Windows {
	toks := Tokens(text)
	if len(toks) > MaxFuzzyTokens {
		toks = toks[:MaxFuzzyTokens]
	}
	return &Windows{tokens: toks, bySize: map[int][]window{}}
}

func (w *Windows) size(n int) []window {
	if ws, ok := w.bySize[n]; ok {
		return ws
	}
	var ws []window
	for i := 0; i+n <= len(w.tokens); i++ {
		j := strings.Join(w.tokens[i:i+n], " ")
		ws = append(ws, window{joined: j, runes: utf8.RuneCountInString(j), start: i})
	}
	w.bySize[n] = ws
	return ws
}

// FuzzyFind looks for a near-miss of phrase in text (both prepared or in Words form); see
// Windows.Find. To match many phrases against one text, build the Windows once.
func FuzzyFind(text, phrase string) (FuzzyHit, bool) {
	return NewWindows(text).Find(phrase)
}

// Find looks for a near-miss of phrase. It compares every window of the text's words with
// one word fewer to one word more than the phrase: a window within MaxEdits edits matches,
// and for phrases of MinTokenSetTokens or more words, a window of the same length with
// token-set similarity of at least MinTokenSetSimilarity matches. An exact occurrence is not
// a fuzzy hit (the keyword matcher reports those). The best hit wins.
func (w *Windows) Find(phrase string) (FuzzyHit, bool) {
	pTokens := Tokens(phrase)
	if len(pTokens) == 0 {
		return FuzzyHit{}, false
	}
	pJoined := strings.Join(pTokens, " ")
	pLen := utf8.RuneCountInString(pJoined)
	maxEdits := MaxEdits(pLen)
	var pSet map[string]bool
	if len(pTokens) >= MinTokenSetTokens {
		pSet = make(map[string]bool, len(pTokens))
		for _, t := range pTokens {
			pSet[t] = true
		}
	}
	best, found := FuzzyHit{}, false
	consider := func(h FuzzyHit) {
		if !found || h.Similarity > best.Similarity {
			best, found = h, true
		}
	}
	for size := max(1, len(pTokens)-1); size <= len(pTokens)+1; size++ {
		for _, win := range w.size(size) {
			if win.joined == pJoined {
				return FuzzyHit{}, false // exact: not a near-miss
			}
			if maxEdits > 0 && win.runes-pLen <= maxEdits && pLen-win.runes <= maxEdits {
				if d, ok := Damerau(win.joined, pJoined, maxEdits); ok {
					consider(FuzzyHit{Matched: win.joined, Similarity: 1 - float64(d)/float64(pLen), Edits: d})
				}
			}
			if pSet != nil && size == len(pTokens) && sharesToken(w.tokens[win.start:win.start+size], pSet) {
				if sim := TokenSetSimilarity(w.tokens[win.start:win.start+size], pTokens); sim >= MinTokenSetSimilarity {
					consider(FuzzyHit{Matched: win.joined, Similarity: sim, Edits: -1})
				}
			}
		}
	}
	return best, found
}

func sharesToken(window []string, set map[string]bool) bool {
	for _, t := range window {
		if set[t] {
			return true
		}
	}
	return false
}
