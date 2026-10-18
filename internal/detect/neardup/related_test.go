package neardup

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"pgregory.net/rapid"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize"
)

func terms(raw string) []string { return Terms(normalize.Text(raw).Norm) }

func TestTerms(t *testing.T) {
	tests := []struct {
		name string
		a, b string // same terms
	}{
		{"function words and greetings", "hello, please send the homework to us", "send homework"},
		{"arabic greetings", "السلام عليكم ورحمة الله وبركاته، حل الواجب", "حل الواجب"},
		{"links and link words", "join our group: https://chat.whatsapp.com/AbCdEf123 assignments", "assignments"},
		{"bare domain links", "at example.com/notes summary", "summary"},
		{"placeholders, keeping a kept number's id", "[phone] [email] [mention] [phone:ab12] essays", "ab12 essays"},
		{"short numbers dropped, long kept", "25% off 12 reports 2026 0551234567", "off reports 2026 0551234567"},
		{"arabic article and conjunction", "والواجبات الواجبات بالواجبات للواجبات", "واجبات"},
		{"three-letter guard", "ولد", "ولد"},
		{"case", "ESSAYS Reports", "essays reports"},
		{"homoglyphs", "frее essays", "free essays"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b := terms(tt.a), terms(tt.b)
			slices.Sort(a)
			slices.Sort(b)
			if !slices.Equal(a, b) {
				t.Fatalf("Terms(%q) = %q, Terms(%q) = %q: want the same terms", tt.a, a, tt.b, b)
			}
		})
	}
	if got := terms("hi https://wa.me/123456 [phone] الله يعطيكم العافية 12"); len(got) != 0 {
		t.Fatalf("noise-only text has terms %q, want none", got)
	}
}

const (
	longAd  = "we offer research papers essays assignments reports presentations projects summaries translation quizzes exams thesis help fast delivery guaranteed grades"
	shortAd = "essays assignments reports presentations projects quizzes"
	moreAd  = "statistics accounting marketing programming engineering medicine nursing pharmacy law biology chemistry physics"
)

func TestRelatedMatch(t *testing.T) {
	tests := []struct {
		name  string
		entry string
		query string
		th    Thresholds
		want  Closeness
	}{
		{"short ad inside a long one", longAd, shortAd, DefaultThresholds(), Related},
		{"too few terms for containment", longAd + " " + moreAd, "essays assignments reports presentations projects", DefaultThresholds(), None},
		{"jaccard path", "essays reports projects", "essays reports projects quizzes exams thesis help fast delivery", DefaultThresholds(), Related},
		{
			"only link and greeting overlap", "hello join https://chat.whatsapp.com/AbCdEf123 crypto signals profits daily",
			"hello join https://chat.whatsapp.com/XyZ987654 calculus study notes tonight", DefaultThresholds(), None,
		},
		{"old thresholds never relate", longAd, shortAd, Thresholds{3, 0.9, 12, 0.4, 0, 0, 0}, None},
		{"identical text stays certain", longAd, longAd, DefaultThresholds(), Certain},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ix := NewIndex(tt.th)
			ix.Add("e", FingerprintText(tt.entry))
			m := ix.Match(FingerprintText(tt.query))
			if m.Closeness != tt.want {
				t.Fatalf("closeness %s (jaccard %.2f, containment %.2f), want %s", m.Closeness, m.Jaccard, m.Containment, tt.want)
			}
		})
	}
}

func TestRelatedAllowListAndComputeOnly(t *testing.T) {
	ix := NewIndex(DefaultThresholds())
	ix.Add("e", FingerprintText(longAd))
	if m := ix.Match(Compute(Key(normalize.Text(shortAd).Norm))); m.Closeness != None {
		t.Fatalf("a fingerprint without terms matched %s: only FingerprintNorm fingerprints can be related", m.Closeness)
	}
	ix.Allow(FingerprintText(shortAd))
	if m := ix.Match(FingerprintText(shortAd)); m.Closeness != None || !m.Allowed {
		t.Fatalf("an overturned text matched %+v, want none (allow-listed)", m)
	}
}

func TestThresholdsValidateRelated(t *testing.T) {
	bad := []Thresholds{
		{3, 0.9, 12, 0.4, -0.1, 0.4, 6},
		{3, 0.9, 12, 0.4, 0.2, 1.5, 6},
		{3, 0.9, 12, 0.4, 0.2, 0.4, -1},
	}
	for _, th := range bad {
		if th.Validate() == nil {
			t.Fatalf("%+v validated, want an error", th)
		}
	}
	if err := DefaultThresholds().Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestRelatedProperties: Related only appears outside the loose limits, never on fingerprints
// without terms, and within the related limits.
func TestRelatedProperties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		word := rapid.SampledFrom(strings.Fields(longAd + " hello the and والواجبات بحوث تقارير مشاريع https://t.me/x 12 2026"))
		gen := rapid.Custom(func(t *rapid.T) string {
			return strings.Join(rapid.SliceOfN(word, 0, 25).Draw(t, "words"), " ")
		})
		th := DefaultThresholds()
		ix := NewIndex(th)
		ix.Add("e", FingerprintText(gen.Draw(t, "entry")))
		q := FingerprintText(gen.Draw(t, "query"))
		m := ix.Match(q)
		if m.Closeness != Related {
			return
		}
		if m.Distance <= th.LooseSimHashMaxDistance || m.Jaccard >= th.LooseMinHashMinJaccard {
			t.Fatalf("related inside the loose limits: %+v", m)
		}
		if len(q.Terms) == 0 || m.Containment < 0 || m.Containment > 1 {
			t.Fatalf("related without terms or with containment out of range: %+v", m)
		}
	})
}

func FuzzTerms(f *testing.F) {
	for _, s := range []string{"", longAd, "والواجبات [phone:ab] https://x.com", "١٢٣ 12 ​​", "ولد و ال"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := Terms(s)
		seen := map[string]bool{}
		for _, term := range got {
			if term == "" || seen[term] || !utf8.ValidString(term) {
				t.Fatalf("Terms(%q) = %q: want distinct, non-empty, valid terms", s, got)
			}
			seen[term] = true
		}
	})
}
