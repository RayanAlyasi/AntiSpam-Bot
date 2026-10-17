package normalize

import (
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

func TestSkeletonMatchesLookalikes(t *testing.T) {
	tests := []struct{ lookalike, plain string }{
		{"frее", "free"},           // Cyrillic е
		{"ρауpаl", "paypal"},       // Greek ρ, Cyrillic а and у
		{"fr\U0001D5BEe", "free"},  // math sans-serif e
		{"сrуptо", "crypto"},       // Cyrillic с, у, о
		{"ВТС", "BTC"},             // Cyrillic В, Т, С
		{"gооgle", "google"},       // Cyrillic о
		{"ոօ", "no"},               // Armenian ո, օ
		{"FRΕΕ", "free"},           // Greek Ε, case-insensitive compare
		{"ＦＲЕЕ", "free"},           // fullwidth plus Cyrillic
		{"pay\u200Bpаl", "paypal"}, // invisible plus Cyrillic
	}
	for _, tt := range tests {
		got, want := Text(tt.lookalike).Skeleton, Text(tt.plain).Skeleton
		if !strings.EqualFold(got, want) {
			t.Errorf("Skeleton(%+q) = %+q, want EqualFold to Skeleton(%q) = %+q (look-alikes must match their Latin form)",
				tt.lookalike, got, tt.plain, want)
		}
	}
}

func TestSingleScriptTextKeepsNorm(t *testing.T) {
	for _, s := range []string{
		"Привет мир", // Привет мир
		"Καλημέρα",   // Καλημέρα
		"مرحبا بالعالم",
		"buy crypto now",
	} {
		r := Text(s)
		if r.Norm != s || r.Stripped != 0 {
			t.Errorf("Text(%q) = Norm %q, Stripped %d; want unchanged and 0 (only Skeleton folds scripts)", s, r.Norm, r.Stripped)
		}
	}
}

func TestPatternMatchesOwnConfusables(t *testing.T) {
	pattern := Text("PayPal").Skeleton
	for _, msg := range []string{"PаyPаl", "ΡayΡal", "ＰａｙＰａｌ"} {
		if got := Text(msg).Skeleton; !strings.EqualFold(got, pattern) {
			t.Errorf("Skeleton(%+q) = %+q, want EqualFold to pattern skeleton %+q", msg, got, pattern)
		}
	}
}

func TestCollapseAndSqueeze(t *testing.T) {
	tests := []struct{ in, want string }{
		{"f.r.e.e", "free"},
		{"f r e e", "free"},
		{"f-r-e-e", "free"},
		{"f . r . e . e", "free"},
		{"f_r_e_e money", "free money"},
		{"get f r e e money", "get free money"},
		{"F•R•E•E", "FREE"},
		{"ر ب ح", "ربح"},
		{"freeeee", "free"},
		{"cooool", "cool"},
		{"ههههه", "هه"},
		{"f r e e e e", "free"},
		// Unchanged.
		{"good", "good"},
		{"1 2 3", "1 2 3"},
		{"1.2.2026", "1.2.2026"},
		{"055 123 4567", "055 123 4567"},
		{"10.0.0.1", "10.0.0.1"},
		{"1000000", "1000000"},
		{"0555", "0555"},
		{"a.b.c.com", "a.b.c.com"},
		{"ab.c.d.e", "ab.c.d.e"},
		{"J. R. R. Tolkien", "J. R. R. Tolkien"},
		{"a b", "a b"},
		{"a.m", "a.m"},
		{"e.g. this", "e.g. this"},
		{"a  b    c", "a  b    c"},
		{"x1.y.z", "x1.y.z"},
	}
	for _, tt := range tests {
		r := Text(tt.in)
		if r.Norm != tt.want || r.Stripped != 0 {
			t.Errorf("Text(%q).Norm = %q, Stripped %d; want %q, 0", tt.in, r.Norm, r.Stripped, tt.want)
		}
	}
}

func TestLeetVariant(t *testing.T) {
	folds := []struct{ in, plain string }{
		{"fr33", "free"},
		{"m0ney", "money"},
		{"cr4zy", "crazy"},
		{"$ale", "sale"},
		{"c@sh", "cash"},
		{"fr1end", "friend"},
		{"f.r.3.3", "free"},
		{"FR33 M0NEY", "free money"},
	}
	for _, tt := range folds {
		r, p := Text(tt.in), Text(tt.plain)
		if !strings.EqualFold(r.Leet, p.Leet) {
			t.Errorf("Text(%q).Leet = %q, want EqualFold to %q", tt.in, r.Leet, p.Leet)
		}
		if strings.EqualFold(r.Norm, p.Norm) && tt.in != strings.ToUpper(tt.plain) {
			t.Errorf("Text(%q).Norm = %q has leet folds; Norm must never get them", tt.in, r.Norm)
		}
	}
	for _, s := range []string{"0555123456", "$100", "US$100", "x100", "2026", "+966", "free", "3.14"} {
		if r := Text(s); r.Leet != r.Norm {
			t.Errorf("Text(%q).Leet = %q, want Norm %q (numbers and prices must not fold)", s, r.Leet, r.Norm)
		}
	}
}

func TestEmojiTokens(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"💰💸 earn 🔥📈", []string{"money_bag", "money_wings", "fire", "chart_up"}},
		{"🔥🔥", []string{"fire", "fire"}},
		{"💰\uFE0F", []string{"money_bag"}},
		{"🤑🏽", []string{"money_mouth"}},
		{"❤\uFE0F 👍🏽 👨\u200D👩\u200D👧", nil},
		{"hello", nil},
	}
	for _, tt := range tests {
		r := Text(tt.in)
		if !slices.Equal(r.EmojiTokens, tt.want) || (tt.want == nil) != (r.EmojiTokens == nil) {
			t.Errorf("Text(%+q).EmojiTokens = %q, want %q", tt.in, r.EmojiTokens, tt.want)
		}
		if !strings.Contains(r.Raw, "💰") && strings.Contains(tt.in, "💰") {
			t.Errorf("Raw lost the emoji")
		}
	}
}

func TestArabicFolds(t *testing.T) {
	same := [][]string{
		{"م\u064Eر\u0652ح\u064Eب\u064Bا", "مرحـــبا", "مرحبا"},
		{"أحمد", "احمد", "إحمد", "آحمد", "ٱحمد", "ا\u0654حمد"},
		{"کتاب", "كتاب"},
		{"ایران", "ايران"},
		{"٠٥٥١٢٣", "۰۵۵۱۲۳", "055123"},
		{"ر ب ح", "ر\u0650ب\u0652ح", "ربـــح", "ربح"},
	}
	for _, group := range same {
		want := Text(group[len(group)-1])
		for _, s := range group {
			if r := Text(s); r.Norm != want.Norm || r.Stripped != 0 {
				t.Errorf("Text(%+q).Norm = %+q, Stripped %d; want %+q, 0 (spelling variants share a Norm; folding is not obfuscation)",
					s, r.Norm, r.Stripped, want.Norm)
			}
		}
	}
	loose := [][2]string{{"مدرسة", "مدرسه"}, {"على", "علي"}}
	for _, p := range loose {
		a, b := Text(p[0]), Text(p[1])
		if a.Loose != b.Loose {
			t.Errorf("Loose(%q) = %q, Loose(%q) = %q; want equal", p[0], a.Loose, p[1], b.Loose)
		}
		if a.Norm == b.Norm {
			t.Errorf("Norm(%q) == Norm(%q); want distinct (ى/ة folds belong only in Loose)", p[0], p[1])
		}
	}
	for _, s := range []string{"hello", "שלום", "नमस\u094Dत\u0947", "یہ اردو ہے"} {
		if r := Text(s); r.Loose != r.Norm {
			t.Errorf("Text(%q).Loose = %q, want Norm %q", s, r.Loose, r.Norm)
		}
	}
	for _, s := range []string{"hello", "ש\u05B8\u05C1לו\u05B9ם", "नमस\u094Dत\u0947"} {
		if r := Text(s); r.Norm != norm.NFKC.String(s) {
			t.Errorf("Text(%q).Norm = %q; non-Arabic text must keep its Norm", s, r.Norm)
		}
	}
}

func TestTatweelJoinedFloodCounted(t *testing.T) {
	in := "ب\u0610\u0611\u0612ـ\u0613\u0614\u0615"
	r := Text(in)
	if r.Norm != "ب" || r.Stripped != 6 {
		t.Errorf("Text(%+q) = %+q, Stripped %d; want \"ب\", 6 (marks joined by removing tatweel are a flood)", in, r.Norm, r.Stripped)
	}
}

func TestTextLinearOnHostileInput(t *testing.T) {
	n := 1 << 18
	for name, in := range map[string]string{
		"spaced letters": strings.Repeat("a ", n/2),
		"dotted leet":    strings.Repeat("3.", n/2),
		"repeats":        strings.Repeat("e", n),
		"tashkeel":       strings.Repeat("ب\u064E\u0651", n/3),
		"mixed":          strings.Repeat("f.r.3.3 💰 ", n/12),
	} {
		start := time.Now()
		Text(in)
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("Text(%s) took %v, want under 3s (normalization must stay linear)", name, d)
		}
	}
}

func FuzzText(f *testing.F) {
	for _, s := range []string{
		"", "f.r.e.e", "fr33 m0ney", "م\u064Eر\u0652ح\u064Eب\u064Bا", "ر ب ح", "frее", "💰🔥", "a.b.c.com",
		"freeeee", "٠٥٥", "ﬁnance", "e\u0301\u0302\u0303\u0304", "ᄀ ᅡ ᆨ",
		"ب\u0610\u0611\u0612ـ\u0613\u0614\u0615",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		r := Text(in)
		if r.Raw != in {
			t.Fatalf("Raw changed")
		}
		for name, v := range map[string]string{"Norm": r.Norm, "Skeleton": r.Skeleton, "Leet": r.Leet, "Loose": r.Loose} {
			if !utf8.ValidString(v) {
				t.Fatalf("Text(%+q).%s = %+q is not valid UTF-8", in, name, v)
			}
		}
		if !norm.NFKC.IsNormalString(r.Norm) || strings.ContainsFunc(r.Norm, isInvisible) {
			t.Fatalf("Text(%+q).Norm = %+q is not NFKC-normal or keeps an invisible", in, r.Norm)
		}
		if r.Stripped < 0 || r.Stripped > utf8.RuneCountInString(in) {
			t.Fatalf("Text(%+q).Stripped = %d out of range", in, r.Stripped)
		}
		if again := Text(r.Norm); again.Norm != r.Norm || again.Stripped != 0 {
			t.Fatalf("Text is not idempotent: Text(%+q).Norm = %+q, Stripped %d", r.Norm, again.Norm, again.Stripped)
		}
	})
}
