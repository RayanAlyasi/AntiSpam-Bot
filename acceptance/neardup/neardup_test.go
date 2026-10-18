package neardup_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/neardup"
)

// explicit pins the loose limits so boundary tests don't depend on the defaults.
var explicit = neardup.Thresholds{
	SimHashMaxDistance: 3, MinHashMinJaccard: 0.9,
	LooseSimHashMaxDistance: 12, LooseMinHashMinJaccard: 0.4,
}

const sigLen = 100

// sig returns a MinHash signature whose values are unique to tag.
func sig(tag uint64) []uint64 {
	s := make([]uint64, sigLen)
	for i := range s {
		s[i] = tag<<32 | uint64(i)
	}
	return s
}

// agree returns a copy of base that agrees with it in exactly k positions (Jaccard estimate k/len).
func agree(base []uint64, k int, tag uint64) []uint64 {
	s := append([]uint64(nil), base...)
	for i := k; i < len(s); i++ {
		s[i] = tag<<32 | uint64(i) | 1<<31
	}
	return s
}

// flip returns x with its lowest n bits inverted (Hamming distance n).
func flip(x uint64, n int) uint64 {
	if n == 64 {
		return ^x
	}
	return x ^ (1<<uint(n) - 1)
}

const entrySim uint64 = 0xA5A5_F00D_1234_5678

func entry() neardup.Fingerprint { return neardup.Fingerprint{SimHash: entrySim, MinHash: sig(1)} }

func closeName(c neardup.Closeness) string {
	return map[neardup.Closeness]string{neardup.None: "none", neardup.Loose: "loose", neardup.Certain: "certain"}[c]
}

func TestDefaultThresholdsMatchDesign(t *testing.T) {
	d := neardup.DefaultThresholds()
	if d.SimHashMaxDistance != 3 || d.MinHashMinJaccard != 0.9 {
		t.Errorf("got certain thresholds %d bits / %.2f, want 3 / 0.90 (DESIGN §7.0, §12.1 detection.certain)", d.SimHashMaxDistance, d.MinHashMinJaccard)
	}
	if d.LooseSimHashMaxDistance <= d.SimHashMaxDistance || d.LooseMinHashMinJaccard >= d.MinHashMinJaccard || d.LooseMinHashMinJaccard <= 0 {
		t.Errorf("got loose thresholds %d bits / %.2f, want strictly looser than certain and above 0 (looser closeness is a rules signal, DESIGN §7.0)", d.LooseSimHashMaxDistance, d.LooseMinHashMinJaccard)
	}
	if err := d.Validate(); err != nil {
		t.Errorf("got Validate error %v for the defaults, want nil", err)
	}
}

func TestThresholdsValidateRejectsUnsafe(t *testing.T) {
	bad := map[string]neardup.Thresholds{
		"negative distance":       {SimHashMaxDistance: -1, MinHashMinJaccard: 0.9, LooseSimHashMaxDistance: 12, LooseMinHashMinJaccard: 0.4},
		"distance above 64":       {SimHashMaxDistance: 65, MinHashMinJaccard: 0.9, LooseSimHashMaxDistance: 65, LooseMinHashMinJaccard: 0.4},
		"jaccard above 1":         {SimHashMaxDistance: 3, MinHashMinJaccard: 1.1, LooseSimHashMaxDistance: 12, LooseMinHashMinJaccard: 0.4},
		"jaccard zero":            {SimHashMaxDistance: 3, MinHashMinJaccard: 0, LooseSimHashMaxDistance: 12, LooseMinHashMinJaccard: 0},
		"loose tighter distance":  {SimHashMaxDistance: 3, MinHashMinJaccard: 0.9, LooseSimHashMaxDistance: 2, LooseMinHashMinJaccard: 0.4},
		"loose tighter jaccard":   {SimHashMaxDistance: 3, MinHashMinJaccard: 0.9, LooseSimHashMaxDistance: 12, LooseMinHashMinJaccard: 0.95},
		"loose jaccard not above": {SimHashMaxDistance: 3, MinHashMinJaccard: 0.9, LooseSimHashMaxDistance: 12, LooseMinHashMinJaccard: -0.1},
	}
	for name, th := range bad {
		if err := th.Validate(); err == nil {
			t.Errorf("%s: got nil from Validate, want an error (unsafe thresholds would turn loose closeness into bans, DESIGN §7.0)", name)
		}
	}
	if err := explicit.Validate(); err != nil {
		t.Errorf("got Validate error %v for %+v, want nil", err, explicit)
	}
}

func TestMatchThresholdBoundaries(t *testing.T) {
	tests := []struct {
		name string
		fp   neardup.Fingerprint
		want neardup.Closeness
		why  string
	}{
		{"simhash 0 bits", neardup.Fingerprint{SimHash: entrySim, MinHash: sig(2)}, neardup.Certain, "SimHash ≤ 3 is CERTAIN, DESIGN §7.0"},
		{"simhash 3 bits", neardup.Fingerprint{SimHash: flip(entrySim, 3), MinHash: sig(2)}, neardup.Certain, "SimHash ≤ 3 is CERTAIN, DESIGN §7.0"},
		{"simhash 4 bits", neardup.Fingerprint{SimHash: flip(entrySim, 4), MinHash: sig(2)}, neardup.Loose, "4 bits is above the tight limit, so only a rules signal, DESIGN §7.0"},
		{"simhash 12 bits", neardup.Fingerprint{SimHash: flip(entrySim, 12), MinHash: sig(2)}, neardup.Loose, "within the loose limit"},
		{"simhash 13 bits", neardup.Fingerprint{SimHash: flip(entrySim, 13), MinHash: sig(2)}, neardup.None, "beyond the loose limit"},
		{"minhash 0.90", neardup.Fingerprint{SimHash: flip(entrySim, 32), MinHash: agree(sig(1), 90, 3)}, neardup.Certain, "MinHash Jaccard ≥ 0.9 is CERTAIN, DESIGN §7.0"},
		{"minhash 0.89", neardup.Fingerprint{SimHash: flip(entrySim, 32), MinHash: agree(sig(1), 89, 3)}, neardup.Loose, "below 0.9 is only a rules signal, DESIGN §7.0"},
		{"minhash 0.40", neardup.Fingerprint{SimHash: flip(entrySim, 32), MinHash: agree(sig(1), 40, 3)}, neardup.Loose, "within the loose limit"},
		{"minhash 0.39", neardup.Fingerprint{SimHash: flip(entrySim, 32), MinHash: agree(sig(1), 39, 3)}, neardup.None, "beyond the loose limit"},
		{"unrelated", neardup.Fingerprint{SimHash: flip(entrySim, 40), MinHash: sig(9)}, neardup.None, "nothing in common"},
	}
	ix := neardup.NewIndex(explicit)
	ix.Add("e1", entry())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := ix.Match(tt.fp)
			if m.Closeness != tt.want {
				t.Fatalf("got %s (distance %d, jaccard %.2f), want %s (%s)", closeName(m.Closeness), m.Distance, m.Jaccard, closeName(tt.want), tt.why)
			}
			if tt.want != neardup.None && m.EntryID != "e1" {
				t.Errorf("got entry %q, want e1 (the match must name the corpus entry it hit, DESIGN §7.3 evidence)", m.EntryID)
			}
		})
	}
}

func TestMatchPrefersCertainEntry(t *testing.T) {
	ix := neardup.NewIndex(explicit)
	ix.Add("loose", neardup.Fingerprint{SimHash: flip(entrySim, 8), MinHash: sig(5)})
	ix.Add("certain", neardup.Fingerprint{SimHash: flip(entrySim, 50), MinHash: agree(sig(1), 95, 6)})

	m := ix.Match(entry())

	if m.Closeness != neardup.Certain || m.EntryID != "certain" {
		t.Errorf("got %s via %q, want certain via \"certain\" (the strongest evidence decides, DESIGN §7.3)", closeName(m.Closeness), m.EntryID)
	}
}

func TestAllowListCheckedFirst(t *testing.T) {
	ix := neardup.NewIndex(explicit)
	ix.Add("e1", entry())
	other := neardup.Fingerprint{SimHash: flip(entrySim, 40), MinHash: sig(7)}
	ix.Add("e2", other)
	overturned := neardup.Fingerprint{SimHash: flip(entrySim, 1), MinHash: agree(sig(1), 97, 8)}
	ix.Allow(overturned)

	if m := ix.Match(overturned); m.Closeness != neardup.None || !m.Allowed {
		t.Errorf("got %s allowed=%v for an overturned fingerprint, want none allowed=true (overturns are checked before the corpus, DESIGN §7.5)", closeName(m.Closeness), m.Allowed)
	}
	nearOverturned := neardup.Fingerprint{SimHash: flip(overturned.SimHash, 2), MinHash: agree(overturned.MinHash, 96, 10)}
	if m := ix.Match(nearOverturned); m.Closeness == neardup.Certain {
		t.Errorf("got certain for a fingerprint within the tight limits of an overturned one, want not certain (so the mistake isn't repeated, DESIGN §7.5)")
	}
	if m := ix.Match(other); m.Closeness != neardup.Certain || m.EntryID != "e2" {
		t.Errorf("got %s via %q for an unrelated confirmed entry, want certain via e2 (an allow-list entry only covers its own pattern, DESIGN §7.5)", closeName(m.Closeness), m.EntryID)
	}
}

func TestRemoveRetractsEntry(t *testing.T) {
	ix := neardup.NewIndex(explicit)
	ix.Add("e1", entry())
	ix.Remove("e1")
	ix.Remove("missing")

	if m := ix.Match(entry()); m.Closeness != neardup.None {
		t.Errorf("got %s via %q after Remove, want none (a retracted entry stops matching at once, DESIGN §7.5)", closeName(m.Closeness), m.EntryID)
	}
}

func TestEmptyFingerprintNeverMatches(t *testing.T) {
	ix := neardup.NewIndex(explicit)
	ix.Add("e1", neardup.Fingerprint{SimHash: 0, MinHash: sig(1)})
	ix.Add("empty", neardup.Compute(""))

	if got := neardup.Compute(""); len(got.MinHash) != 0 {
		t.Errorf("got a %d-slot MinHash for empty text, want none (no tokens, no fingerprint)", len(got.MinHash))
	}
	for name, fp := range map[string]neardup.Fingerprint{"zero": {}, "computed empty": neardup.Compute("")} {
		if m := ix.Match(fp); m.Closeness != neardup.None {
			t.Errorf("%s: got %s via %q, want none (text with no tokens must never count as known spam, DESIGN §7.0)", name, closeName(m.Closeness), m.EntryID)
		}
	}
}

const template = "Dear members, we are excited to announce an exclusive investment opportunity for this group only. " +
	"Our licensed crypto trading team has helped hundreds of families earn between five hundred and two thousand dollars every week " +
	"without any experience. You only need a phone and thirty minutes a day. Our expert mentor will guide you step by step, " +
	"and all profits are paid directly to your bank account every Monday. Spaces are limited because we want to give personal " +
	"attention to each new investor. Hundreds of people have already joined and changed their lives. Do not miss this chance " +
	"to secure your financial future and support your family. Send a message to our manager on WhatsApp today and write the " +
	"word INVEST to receive the full details, the registration form and your welcome bonus."

const rewrite = "Dear members, we are excited to announce an exclusive investment opportunity for this group only. " +
	"Our licensed crypto trading team has helped hundreds of families earn between five hundred and two thousand dollars every week " +
	"without any experience. You only need a phone and thirty minutes a day. Our expert mentor will guide you step by step, " +
	"and all profits are paid directly to your bank account every Monday. Spaces are limited because we want to give personal " +
	"attention to each new investor. Hundreds of people have already joined and changed their lives. Message our support desk now, " +
	"mention this group, and you will get a free first lesson, a beginner guide and a seat in tonight's live webinar with our senior analyst."

func TestComputeOnRealText(t *testing.T) {
	tests := []struct {
		name, text string
		want       neardup.Closeness
		why        string
	}{
		{"identical", template, neardup.Certain, "the same text is CERTAIN known spam, DESIGN §7.0"},
		{"one word changed", strings.Replace(template, "Monday", "Friday", 1), neardup.Certain, "a template reused with a small edit is within the tight limits, DESIGN §7.5"},
		{"last third rewritten", rewrite, neardup.Loose, "related but heavily changed text is only a rules signal for the AI, DESIGN §7.0"},
		{"unrelated chat", "Hi everyone, the football match moved to Saturday at 5pm, please bring water and your kit.", neardup.None, "normal chat must not look like the corpus, DESIGN §8"},
	}
	ix := neardup.NewIndex(neardup.DefaultThresholds())
	ix.Add("tmpl", neardup.Compute(template))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if a, b := neardup.Compute(tt.text), neardup.Compute(tt.text); a.SimHash != b.SimHash || fmt.Sprint(a.MinHash) != fmt.Sprint(b.MinHash) {
				t.Fatalf("got different fingerprints for the same text, want deterministic Compute (stored fingerprints must keep matching)")
			}
			m := ix.Match(neardup.Compute(tt.text))
			if m.Closeness != tt.want {
				t.Errorf("got %s (distance %d, jaccard %.2f), want %s (%s)", closeName(m.Closeness), m.Distance, m.Jaccard, closeName(tt.want), tt.why)
			}
		})
	}
}

func TestIndexConcurrentUse(t *testing.T) {
	ix := neardup.NewIndex(explicit)
	ix.Add("base", entry())
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 200 {
				id := fmt.Sprintf("g%d-%d", g, i)
				fp := neardup.Fingerprint{SimHash: uint64(g)<<40 | uint64(i), MinHash: sig(uint64(100 + g*1000 + i))}
				ix.Add(id, fp)
				ix.Match(fp)
				if i%10 == 0 {
					ix.Allow(neardup.Fingerprint{SimHash: ^fp.SimHash, MinHash: sig(uint64(900000 + g*1000 + i))})
				}
				ix.Remove(id)
			}
		})
	}
	wg.Wait()
	if m := ix.Match(entry()); m.Closeness != neardup.Certain || m.EntryID != "base" {
		t.Errorf("got %s via %q after concurrent use, want certain via base (the index is shared by concurrent workers)", closeName(m.Closeness), m.EntryID)
	}
}
