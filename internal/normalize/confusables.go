package normalize

import (
	_ "embed"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/text/unicode/norm"
)

// confusablesTxt is Unicode TR39 confusables.txt, version 16.0.0, embedded
// unchanged under the Unicode licence (see data/README.md).
//
//go:embed data/confusables.txt
var confusablesTxt string

var confusables = sync.OnceValue(func() map[rune]string {
	return parseConfusables(confusablesTxt)
})

// parseConfusables reads lines of the form "0430 ;\t0061 ;\tMA\t# ...".
func parseConfusables(txt string) map[rune]string {
	m := make(map[rune]string, 6500)
	for line := range strings.Lines(txt) {
		line, _, _ = strings.Cut(line, "#")
		src, rest, ok := strings.Cut(line, ";")
		if !ok {
			continue
		}
		dst, _, _ := strings.Cut(rest, ";")
		from, err := strconv.ParseUint(strings.TrimSpace(src), 16, 32)
		if err != nil {
			continue
		}
		var b strings.Builder
		for _, f := range strings.Fields(dst) {
			r, err := strconv.ParseUint(f, 16, 32)
			if err != nil {
				b.Reset()
				break
			}
			b.WriteRune(rune(r))
		}
		if b.Len() > 0 {
			m[rune(from)] = b.String()
		}
	}
	return m
}

// Skeleton returns the TR39 skeleton of s: NFD, each character replaced by
// its confusable prototype, NFD again. Look-alikes from other scripts share
// a skeleton (frее with Cyrillic е and free). Prototypes mix case, so compare
// skeletons with strings.EqualFold.
func Skeleton(s string) string {
	table := confusables()
	d := norm.NFD.String(s)
	var b strings.Builder
	b.Grow(len(d))
	for _, r := range d {
		if p, ok := table[r]; ok {
			b.WriteString(p)
			continue
		}
		b.WriteRune(r)
	}
	return norm.NFD.String(b.String())
}
