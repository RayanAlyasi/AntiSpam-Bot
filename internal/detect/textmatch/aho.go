package textmatch

import (
	"cmp"
	"slices"
)

// Hit is one keyword or phrase match: the index of the pattern in the slice the automaton was
// built from, and the byte span of the match in the searched (Words form) text.
type Hit struct {
	Pattern    int
	Start, End int
}

// Automaton is an Aho-Corasick automaton over Words-form patterns. It finds every occurrence
// of every pattern in one pass over the text, so thousands of patterns cost microseconds.
// It is immutable and safe for concurrent use.
type Automaton struct {
	nodes    []acNode
	patterns []string
	whole    []bool
}

type acNode struct {
	next map[byte]int32
	fail int32
	out  []int32 // patterns ending here, including through failure links
}

// Keyword is one pattern for NewAutomaton. WholeWord matches only when the pattern isn't
// glued to letters or digits on either side ("free" doesn't match "freedom"); otherwise any
// occurrence matches.
type Keyword struct {
	Text      string
	WholeWord bool
}

// NewAutomaton builds an automaton. Patterns are converted with Words; patterns that are
// empty after that (only punctuation or emoji) never match. Hit.Pattern indexes keywords.
func NewAutomaton(keywords []Keyword) *Automaton {
	a := &Automaton{nodes: []acNode{{}}, patterns: make([]string, len(keywords)), whole: make([]bool, len(keywords))}
	for i, k := range keywords {
		p := Words(k.Text)
		a.patterns[i] = p
		a.whole[i] = k.WholeWord
		if p == "" {
			continue
		}
		n := int32(0)
		for j := 0; j < len(p); j++ {
			next, ok := a.nodes[n].next[p[j]]
			if !ok {
				if a.nodes[n].next == nil {
					a.nodes[n].next = map[byte]int32{}
				}
				next = int32(len(a.nodes))
				a.nodes = append(a.nodes, acNode{})
				a.nodes[n].next[p[j]] = next
			}
			n = next
		}
		a.nodes[n].out = append(a.nodes[n].out, int32(i))
	}
	a.link()
	return a
}

// link computes failure links breadth first and merges each node's outputs with those of
// its failure target.
func (a *Automaton) link() {
	queue := make([]int32, 0, len(a.nodes))
	for _, child := range a.nodes[0].next {
		queue = append(queue, child) // fail = 0 already
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for b, child := range a.nodes[n].next {
			f := a.nodes[n].fail
			for {
				if t, ok := a.nodes[f].next[b]; ok {
					a.nodes[child].fail = t
					break
				}
				if f == 0 {
					break
				}
				f = a.nodes[f].fail
			}
			a.nodes[child].out = append(a.nodes[child].out, a.nodes[a.nodes[child].fail].out...)
			queue = append(queue, child)
		}
	}
}

// Len returns the number of patterns the automaton was built from.
func (a *Automaton) Len() int { return len(a.patterns) }

// Pattern returns the Words form of pattern i.
func (a *Automaton) Pattern(i int) string { return a.patterns[i] }

// Find returns every match in text, which must already be in Words form, ordered by start, then
// end, then pattern index. Overlapping matches of different patterns are all reported.
func (a *Automaton) Find(text string) []Hit {
	var hits []Hit
	n := int32(0)
	for i := 0; i < len(text); i++ {
		b := text[i]
		for {
			if t, ok := a.nodes[n].next[b]; ok {
				n = t
				break
			}
			if n == 0 {
				break
			}
			n = a.nodes[n].fail
		}
		for _, p := range a.nodes[n].out {
			end := i + 1
			start := end - len(a.patterns[p])
			if a.whole[p] && !atWordBoundary(text, start, end) {
				continue
			}
			hits = append(hits, Hit{Pattern: int(p), Start: start, End: end})
		}
	}
	slices.SortFunc(hits, func(x, y Hit) int {
		return cmp.Or(cmp.Compare(x.Start, y.Start), cmp.Compare(x.End, y.End), cmp.Compare(x.Pattern, y.Pattern))
	})
	return hits
}
