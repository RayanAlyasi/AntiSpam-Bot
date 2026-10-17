package normalize

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Result is the normalized form of one text surface (DESIGN.md §7.2).
// Later normalization features add fields; callers read fields by name.
type Result struct {
	// Raw is the input, unchanged, kept for evidence.
	Raw string
	// Norm is the form used for matching.
	Norm string
	// Stripped counts the invisible and combining-flood characters removed as
	// obfuscation. It is a signal input.
	Stripped int
	// Skeleton is the TR39 confusables skeleton of Norm; compare it with
	// strings.EqualFold.
	Skeleton string
	// Leet is Norm with digits and symbols used as letters folded.
	Leet string
	// Loose is Norm with the lossy Arabic folds ى→ي and ة→ه.
	Loose string
	// EmojiTokens has one token per spam emoji, in order; nil when none.
	EmojiTokens []string
}

// Text normalizes one text surface. Matchers prepare their patterns with Text
// too and compare the same field.
func Text(raw string) Result {
	folded, stripped := Fold(raw)
	n, floods := matchForm(folded)
	stripped = min(stripped+floods, utf8.RuneCountInString(raw))
	return Result{
		Raw:         raw,
		Norm:        n,
		Stripped:    stripped,
		Skeleton:    Skeleton(n),
		Leet:        Leet(n),
		Loose:       LooseArabic(n),
		EmojiTokens: EmojiTokens(n),
	}
}

// matchForm applies the folds that make up Norm after Fold: Arabic and
// Persian folding, separator collapsing and repeat squeezing. The folds
// themselves are never counted as obfuscation. Removing tatweel can join two
// mark runs into a flood (ب+marks+ـ+marks), so floods are stripped and
// counted again here, and removing marks or gaps can let NFKC compose again,
// so it repeats until the result is stable.
func matchForm(s string) (string, int) {
	floods := 0
	for range maxFoldPasses {
		t, n := StripFloods(SqueezeRepeats(CollapseSeparators(FoldArabic(s))))
		floods += n
		t = norm.NFKC.String(t)
		if t == s {
			break
		}
		s = t
	}
	return s, floods
}

// maxFoldPasses bounds the NFKC/flood loop in Fold. Real input settles in two
// passes; the extra ones cover compatibility characters that NFKC turns into
// marks or fillers.
const maxFoldPasses = 8

// Fold applies DESIGN.md §7.2 steps 1 and 2: it removes invisible characters,
// strips combining-mark floods and applies NFKC, repeating until the result
// is stable. It returns valid, NFKC-normal UTF-8 and the number of input
// characters removed as obfuscation.
func Fold(raw string) (string, int) {
	if isPlainASCII(raw) {
		return raw, 0
	}
	s, stripped := StripInvisibles(raw)
	for range maxFoldPasses {
		t, n := StripFloods(s)
		stripped += n
		t = norm.NFKC.String(t)
		if strings.ContainsFunc(t, isInvisible) {
			// NFKC maps a few compatibility fillers to invisible ones, and inserts
			// U+034F into very long runs of non-starters. Neither came from the
			// input as an invisible, so neither is counted.
			s = removeInvisibles(t)
			continue
		}
		if n == 0 && t == s {
			break
		}
		s = t
	}
	// A mark that NFKC expanded can be counted more than once; the count never
	// exceeds the characters the sender typed.
	return s, min(stripped, utf8.RuneCountInString(raw))
}

func isPlainASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
