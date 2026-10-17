package textmatch

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

func TestWords(t *testing.T) {
	tests := []struct{ in, want string }{
		{"  Free\tBITCOIN\n\nnow  ", "free bitcoin now"},
		{"free-bitcoin!!", "free bitcoin"},
		{"اربح   المال، الآن", "اربح المال الآن"},
		{"💰💰", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := Words(tt.in); got != tt.want {
			t.Errorf("Words(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAutomatonFind(t *testing.T) {
	kws := []Keyword{
		{Text: "free", WholeWord: true},
		{Text: "Free Bitcoin", WholeWord: true},
		{Text: "bitcoin"},
		{Text: "اربح المال", WholeWord: true},
		{Text: "coin"},
		{Text: "!!!"}, // empty in Words form: never matches
	}
	a := NewAutomaton(kws)
	tests := []struct {
		name string
		text string
		want []string // matched patterns in order
	}{
		{"overlapping phrase and words", "get FREE bitcoin now", []string{"free", "free bitcoin", "bitcoin", "coin"}},
		{"whole word is not inside a longer word", "freedom of speech", nil},
		{"substring keyword matches inside a word", "mybitcoins", []string{"bitcoin", "coin"}},
		{"punctuation between words", "free-bitcoin!!!", []string{"free", "free bitcoin", "bitcoin", "coin"}},
		{"arabic phrase", "اربح المال بسرعة", []string{"اربح المال"}},
		{"arabic phrase glued to a prefix", "واربح المال", nil},
		{"nothing", "see you at the library", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := Words(tt.text)
			var got []string
			for _, h := range a.Find(text) {
				if text[h.Start:h.End] != a.Pattern(h.Pattern) {
					t.Fatalf("hit span %q != pattern %q", text[h.Start:h.End], a.Pattern(h.Pattern))
				}
				got = append(got, a.Pattern(h.Pattern))
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("Find(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
	if a.Len() != len(kws) {
		t.Errorf("Len = %d, want %d", a.Len(), len(kws))
	}
}

// Property: the automaton finds exactly what a naive scan finds.
func TestAutomatonMatchesNaiveScan(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 9))
	alphabet := []string{"a", "b", "ab", "ba", " ", "ع", "ب"}
	gen := func(n int) string {
		var b strings.Builder
		for range n {
			b.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		return b.String()
	}
	for range 200 {
		var kws []Keyword
		for range 1 + rng.IntN(8) {
			kws = append(kws, Keyword{Text: gen(1 + rng.IntN(3)), WholeWord: rng.IntN(2) == 0})
		}
		a := NewAutomaton(kws)
		text := Words(gen(20))
		want := 0
		for i := range kws {
			p := a.Pattern(i)
			if p == "" {
				continue
			}
			for s := 0; s+len(p) <= len(text); s++ {
				if text[s:s+len(p)] == p && (!kws[i].WholeWord || atWordBoundary(text, s, s+len(p))) {
					want++
				}
			}
		}
		if got := len(a.Find(text)); got != want {
			t.Fatalf("Find(%q) with %v found %d, naive scan %d", text, kws, got, want)
		}
	}
}

func TestAutomatonManyPatternsIsFast(t *testing.T) {
	var kws []Keyword
	for i := range 5000 {
		kws = append(kws, Keyword{Text: fmt.Sprintf("spamword%d offer", i), WholeWord: true})
	}
	a := NewAutomaton(kws)
	text := Words(strings.Repeat("hello there this is an ordinary message about nothing ", 20) + "spamword4242 offer")
	start := time.Now()
	hits := a.Find(text)
	if el := time.Since(start); el > 2*time.Second {
		t.Errorf("Find over 5000 patterns took %v, want one linear pass (T0 budget)", el)
	}
	if len(hits) != 1 || a.Pattern(hits[0].Pattern) != "spamword4242 offer" {
		t.Errorf("hits = %v, want the one planted phrase", hits)
	}
}

func TestCompileRegex(t *testing.T) {
	tests := []struct {
		pattern string
		wantErr error
		match   string
		noMatch string
	}{
		{pattern: `earn \d+ (usd|dollars?) daily`, match: "EARN 500 dollars daily", noMatch: "earn some money"},
		{pattern: `t\.me/\w+`, match: "join t.me/signals", noMatch: "tme"},
		{pattern: ``, wantErr: ErrRegexEmpty},
		{pattern: strings.Repeat("a", MaxRegexLen+1), wantErr: ErrRegexTooLong},
		{pattern: `((abc|def|ghi){30}){30}`, wantErr: ErrRegexTooComplex},
		{pattern: `((a{100}){100})`, wantErr: ErrRegexInvalid}, // RE2 caps total repetition at 1000
		{pattern: `(`, wantErr: ErrRegexInvalid},
		{pattern: `a{1001}`, wantErr: ErrRegexInvalid},
		{pattern: `(?P<x>a)\1`, wantErr: ErrRegexInvalid}, // backreferences aren't RE2
		{pattern: `a*`, wantErr: ErrRegexMatchesEmpty},
		{pattern: `^|spam`, wantErr: ErrRegexMatchesEmpty},
	}
	for _, tt := range tests {
		t.Run(tt.pattern[:min(len(tt.pattern), 20)], func(t *testing.T) {
			re, err := CompileRegex(tt.pattern)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("CompileRegex err = %v, want %v (unsafe patterns are rejected on save)", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CompileRegex err = %v, want nil", err)
			}
			if !re.MatchString(tt.match) || re.MatchString(tt.noMatch) {
				t.Errorf("match %q = %v, %q = %v; want true, false", tt.match, re.MatchString(tt.match), tt.noMatch, re.MatchString(tt.noMatch))
			}
		})
	}
}

func TestRegexIsLinearOnHostileInput(t *testing.T) {
	re, err := CompileRegex(`(a|aa)+b`)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	re.MatchString(strings.Repeat("a", 1<<16))
	if el := time.Since(start); el > 2*time.Second {
		t.Errorf("match took %v, want linear time (RE2)", el)
	}
}

func TestDamerau(t *testing.T) {
	tests := []struct {
		a, b  string
		limit int
		want  int
		ok    bool
	}{
		{"bitcoin", "bitcoin", 2, 0, true},
		{"bitcoin", "bitcion", 2, 1, true}, // transposition
		{"bitcoin", "bitcoins", 2, 1, true},
		{"bitcoin", "btcoin", 2, 1, true},
		{"bitcoin", "litecoin", 2, 2, true},
		{"bitcoin", "dogecoin", 2, 3, false},
		{"ربح", "ربخ", 1, 1, true},
		{"", "abc", 5, 3, true},
		{"abc", "abcdefgh", 2, 3, false},
	}
	for _, tt := range tests {
		got, ok := Damerau(tt.a, tt.b, tt.limit)
		if got != tt.want || ok != tt.ok {
			t.Errorf("Damerau(%q, %q, %d) = %d, %v; want %d, %v", tt.a, tt.b, tt.limit, got, ok, tt.want, tt.ok)
		}
	}
}

func TestTokenSetSimilarity(t *testing.T) {
	a := []string{"free", "bitcoin", "giveaway"}
	if s := TokenSetSimilarity(a, []string{"giveaway", "free", "bitcoin", "free"}); s != 1 {
		t.Errorf("reordered with repeat = %v, want 1", s)
	}
	if s := TokenSetSimilarity(a, []string{"free", "bitcoin", "today"}); s != 0.5 {
		t.Errorf("two of four shared = %v, want 0.5", s)
	}
	if s := TokenSetSimilarity(nil, a); s != 0 {
		t.Errorf("empty = %v, want 0", s)
	}
}

func TestFuzzyFind(t *testing.T) {
	tests := []struct {
		name, text, phrase string
		want               bool
		matched            string
	}{
		{"one typo", "claim your free bitcion today", "free bitcoin", true, "free bitcion"},
		{"split word", "join the crypto give away now", "crypto giveaway", true, "crypto give away"},
		{"reordered phrase", "giveaway bitcoin free for all", "free bitcoin giveaway", true, "giveaway bitcoin free"},
		{"arabic typo", "اربح الماال بسرعة", "اربح المال", true, "اربح الماال"},
		{"exact is not fuzzy", "free bitcoin here", "free bitcoin", false, ""},
		{"short phrase never fuzzy", "this is a test of the bot", "bat", false, ""},
		{"unrelated", "see you at the library tomorrow", "free bitcoin", false, ""},
		{"real word one edit from a short keyword", "i need a loan", "loans", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, ok := FuzzyFind(Words(tt.text), Words(tt.phrase))
			if ok != tt.want {
				t.Fatalf("FuzzyFind ok = %v (%+v), want %v", ok, h, tt.want)
			}
			if ok && h.Matched != tt.matched {
				t.Errorf("Matched = %q, want %q", h.Matched, tt.matched)
			}
			if ok && (h.Similarity <= 0 || h.Similarity > 1) {
				t.Errorf("Similarity = %v, want in (0, 1]", h.Similarity)
			}
		})
	}
}

func FuzzMatchers(f *testing.F) {
	for _, s := range []string{"free bitcoin", "", "اربح", "\xff", "a  b", "a*"} {
		f.Add(s, "free bitcoin")
	}
	f.Fuzz(func(t *testing.T, text, pattern string) {
		w := Words(text)
		if Words(w) != w {
			t.Fatalf("Words not idempotent on %q", text)
		}
		a := NewAutomaton([]Keyword{{Text: pattern, WholeWord: true}, {Text: pattern}})
		for _, h := range a.Find(w) {
			if h.Start < 0 || h.End > len(w) || w[h.Start:h.End] != a.Pattern(h.Pattern) {
				t.Fatalf("bad hit %+v in %q", h, w)
			}
		}
		FuzzyFind(w, Words(pattern))
		if re, err := CompileRegex(pattern); err == nil {
			re.MatchString(w)
		}
	})
}
