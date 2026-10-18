package neardup

import (
	"math"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

const template = "Congratulations you have been selected for our exclusive crypto investment program " +
	"earn five hundred dollars every single day from home with no experience needed just " +
	"message our manager on whatsapp today and start receiving guaranteed profits this week"

func TestComputeTokens(t *testing.T) {
	tests := []struct {
		name  string
		a, b  string
		equal bool
	}{
		{"case is ignored", "Free Money NOW", "free money now", true},
		{"punctuation and spacing are ignored", "free, money!!  now", "free money now", true},
		{"word order changes simhash only", "money free now", "free money now", false},
		{"digits are tokens", "call 0551234567", "call 0551234568", false},
		{"arabic letters and marks are tokens", "ربح سريع", "ربح  سريع!", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b := Compute(tt.a), Compute(tt.b)
			if got := Distance(a, b) == 0 && Jaccard(a, b) == 1; got != tt.equal {
				t.Fatalf("Compute(%q) vs Compute(%q): distance %d, jaccard %.2f; want equal=%v",
					tt.a, tt.b, Distance(a, b), Jaccard(a, b), tt.equal)
			}
		})
	}
	if fp := Compute(" ,.!? "); !fp.Empty() || fp.SimHash != 0 {
		t.Fatalf("Compute of punctuation only = %+v, want the empty fingerprint (no tokens never match)", fp)
	}
}

func TestComputeSeparatesEditsFromRewrites(t *testing.T) {
	words := strings.Fields(template)
	oneWord := strings.Replace(template, "crypto", "bitcoin", 1)
	rewritten := strings.Join(append(words[:len(words)*2/3:len(words)*2/3], "hello", "friends", "please", "read",
		"this", "carefully", "because", "it", "matters", "to", "everyone"), " ")
	unrelated := "the meeting is moved to thursday at five, bring the documents for the school trip"

	base := Compute(template)
	ix := NewIndex(DefaultThresholds())
	ix.Add("e1", base)
	tests := []struct {
		name string
		text string
		want Closeness
	}{
		{"identical", template, Certain},
		{"one word changed", oneWord, Certain},
		{"last third rewritten", rewritten, Loose},
		{"unrelated", unrelated, None},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := Compute(tt.text)
			if m := ix.Match(fp); m.Closeness != tt.want {
				t.Fatalf("closeness %v (distance %d, jaccard %.3f), want %v", m.Closeness, Distance(fp, base), Jaccard(fp, base), tt.want)
			}
		})
	}
}

func TestMatchAllowListSuppresses(t *testing.T) {
	ix := NewIndex(DefaultThresholds())
	ix.Add("spam", Compute(template))
	ix.Allow(Compute(template))
	if m := ix.Match(Compute(template)); m.Closeness != None || !m.Allowed {
		t.Fatalf("Match after Allow = %+v, want suppressed (an overturned fingerprint must never match again)", m)
	}
	if m := ix.Match(Compute(strings.Replace(template, "crypto", "bitcoin", 1))); m.Closeness != None {
		t.Fatalf("Match of a near variant of an allow-listed text = %+v, want none", m)
	}
	ix.Allow(Fingerprint{})
	ix.Add("empty", Fingerprint{})
	if ix.Len() != 1 {
		t.Fatalf("Len = %d, want 1: empty fingerprints are ignored", ix.Len())
	}
}

func TestNewIndexFallsBackOnInvalidThresholds(t *testing.T) {
	bad := Thresholds{SimHashMaxDistance: 40, MinHashMinJaccard: 0.1, LooseSimHashMaxDistance: 12, LooseMinHashMinJaccard: 0.4}
	if NewIndex(bad).Thresholds() != DefaultThresholds() {
		t.Fatal("an invalid threshold set must fall back to the defaults, never loosen CERTAIN matching")
	}
	nan := DefaultThresholds()
	nan.MinHashMinJaccard = math.NaN()
	if nan.Validate() == nil {
		t.Fatal("NaN Jaccard must be rejected")
	}
}

// TestMatchProperties: the closeness of a match follows the thresholds exactly, a smaller
// corpus never yields a closer match, and the empty fingerprint never matches.
func TestMatchProperties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		word := rapid.SampledFrom(strings.Fields(template + " alpha beta gamma delta school meeting tomorrow"))
		gen := rapid.Custom(func(t *rapid.T) string {
			return strings.Join(rapid.SliceOfN(word, 0, 30).Draw(t, "words"), " ")
		})
		texts := rapid.SliceOfN(gen, 1, 6).Draw(t, "corpus")
		query := Compute(gen.Draw(t, "query"))
		th := DefaultThresholds()
		ix := NewIndex(th)
		fps := map[string]Fingerprint{}
		for i, s := range texts {
			id := string(rune('a' + i))
			fps[id] = Compute(s)
			ix.Add(id, fps[id])
		}
		m := ix.Match(query)
		if query.Empty() && m.Closeness != None {
			t.Fatalf("empty query matched %+v", m)
		}
		if m.Closeness == Certain && m.Distance > th.SimHashMaxDistance && m.Jaccard < th.MinHashMinJaccard {
			t.Fatalf("certain match outside the certain limits: %+v", m)
		}
		if m.Closeness == Loose && (m.Distance <= th.SimHashMaxDistance || m.Jaccard >= th.MinHashMinJaccard) && fps[m.EntryID].Tokens >= MinCertainTokens {
			t.Fatalf("loose match inside the certain limits for an entry of %d tokens: %+v", fps[m.EntryID].Tokens, m)
		}
		if m.Closeness == Certain && fps[m.EntryID].Tokens < MinCertainTokens {
			t.Fatalf("certain match on a %d-token entry: short entries may only match loosely", fps[m.EntryID].Tokens)
		}
		ix.Remove("a")
		if m2 := ix.Match(query); m2.Closeness > m.Closeness {
			t.Fatalf("removing an entry made the match closer: %+v → %+v", m, m2)
		}
	})
}

func FuzzCompute(f *testing.F) {
	for _, s := range []string{"", template, "ربح سريع", "​​", "á́́"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		a, b := Compute(s), Compute(s)
		if Distance(a, b) != 0 || (!a.Empty() && Jaccard(a, b) != 1) {
			t.Fatalf("Compute(%q) is not deterministic", s)
		}
		if !a.Empty() && len(a.MinHash) != minHashSize {
			t.Fatalf("MinHash length %d, want %d", len(a.MinHash), minHashSize)
		}
	})
}

func TestFingerprintTextSeesThroughObfuscation(t *testing.T) {
	base := FingerprintText(template)
	ix := NewIndex(DefaultThresholds())
	ix.Add("c1", base)
	for name, variant := range map[string]string{
		"zero-width": strings.ReplaceAll(template, "crypto", "cr​ypto"),
		"homoglyphs": strings.ReplaceAll(template, "e", "е"), // Cyrillic е
		"fullwidth":  strings.Replace(template, "Congratulations", "Ｃｏｎｇｒａｔｕｌａｔｉｏｎｓ", 1),
		"shouting":   strings.ToUpper(template),
		"tatweel":    "ربـــح سريع من البيت كل يوم بدون خبرة راسلنا الآن على الواتساب",
	} {
		t.Run(name, func(t *testing.T) {
			if name == "tatweel" {
				ix.Add("ar", FingerprintText("ربح سريع من البيت كل يوم بدون خبرة راسلنا الآن على الواتساب"))
			}
			if m := ix.Match(FingerprintText(variant)); m.Closeness != Certain {
				t.Fatalf("variant matched %+v, want certain (obfuscation must not hide confirmed spam, DESIGN §7.2)", m)
			}
		})
	}
}

func TestShortEntriesOnlyMatchLoosely(t *testing.T) {
	ix := NewIndex(DefaultThresholds())
	ix.Add("short", FingerprintText("thank you all"))
	if m := ix.Match(FingerprintText("Thank you all!!")); m.Closeness != Loose {
		t.Fatalf("near-copy of a 3-token entry = %+v, want loose: a short confirmed message must never make common phrases known spam", m)
	}
}
