package fingerprint

import (
	"math/rand/v2"
	"strings"
	"testing"
)

const campaign = "Earn 500 dollars a day from home with our crypto trading bot, message me now on WhatsApp"

func TestSimHashDistances(t *testing.T) {
	tests := []struct {
		name    string
		a, b    string
		maxDist int // want Hamming(a, b) <= maxDist
		minDist int // and >= minDist
	}{
		{"identical", campaign, campaign, 0, 0},
		{"punctuation and spacing", campaign, "  EARN 500 dollars, a day... from home with our CRYPTO trading bot!! message me now on WhatsApp ", 0, 0},
		{"one word changed", campaign, strings.Replace(campaign, "500", "700", 1), 8, 0},
		{"one word appended", campaign, campaign + " today", 8, 0},
		{"unrelated", campaign, "Are we still meeting at the library tomorrow afternoon to finish the chemistry homework?", 64, 15},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, okA := ComputeSimHash(tt.a)
			b, okB := ComputeSimHash(tt.b)
			if !okA || !okB {
				t.Fatalf("ComputeSimHash ok = %v, %v, want true (text has letters)", okA, okB)
			}
			if d := Hamming(a, b); d > tt.maxDist || d < tt.minDist {
				t.Errorf("Hamming = %d, want %d–%d (near-duplicates must be close, unrelated text far)", d, tt.minDist, tt.maxDist)
			}
		})
	}
}

func TestEmptyTextHasNoFingerprint(t *testing.T) {
	for _, s := range []string{"", "   ", "!!! ... 🔥🔥", "\u200b"} {
		if _, ok := ComputeSimHash(s); ok {
			t.Errorf("ComputeSimHash(%q) ok = true, want false (no letters or digits: nothing to fingerprint)", s)
		}
		if _, ok := ComputeMinHash(s); ok {
			t.Errorf("ComputeMinHash(%q) ok = true, want false", s)
		}
	}
}

func TestShortTextFingerprints(t *testing.T) {
	a, ok := ComputeSimHash("hi")
	if !ok {
		t.Fatal("ComputeSimHash(hi) ok = false, want true (short text is one shingle)")
	}
	b, _ := ComputeSimHash("HI!")
	if a != b {
		t.Errorf("SimHash(hi) != SimHash(HI!), want equal (case and punctuation ignored)")
	}
}

func TestArabicFingerprints(t *testing.T) {
	msg := "اربح ٥٠٠ دولار يوميا من البيت تواصل معي على الواتساب الآن"
	a, _ := ComputeSimHash(msg)
	b, _ := ComputeSimHash(msg + " بسرعة")
	if d := Hamming(a, b); d > 10 {
		t.Errorf("Hamming = %d for an Arabic message with one word appended, want <= 10", d)
	}
	ma, _ := ComputeMinHash(msg)
	mb, _ := ComputeMinHash(msg + " بسرعة")
	if j := Jaccard(&ma, &mb); j < 0.75 {
		t.Errorf("Jaccard = %.2f, want >= 0.75", j)
	}
}

func TestMinHashJaccard(t *testing.T) {
	a, _ := ComputeMinHash(campaign)
	same, _ := ComputeMinHash(strings.ToUpper(campaign))
	near, _ := ComputeMinHash(strings.Replace(campaign, "500", "700", 1))
	far, _ := ComputeMinHash("Are we still meeting at the library tomorrow afternoon to finish the chemistry homework?")
	if j := Jaccard(&a, &same); j != 1 {
		t.Errorf("Jaccard(same text) = %v, want 1", j)
	}
	if j := Jaccard(&a, &near); j < 0.8 || j == 1 {
		t.Errorf("Jaccard(one word changed) = %v, want in [0.8, 1)", j)
	}
	if j := Jaccard(&a, &far); j > 0.2 {
		t.Errorf("Jaccard(unrelated) = %v, want <= 0.2", j)
	}
}

func TestMinHashBytesRoundTrip(t *testing.T) {
	m, _ := ComputeMinHash(campaign)
	got, err := ParseMinHash(m.Bytes())
	if err != nil || got != m {
		t.Fatalf("ParseMinHash(Bytes()) = %v, %v, want the original signature", got == m, err)
	}
	if _, err := ParseMinHash(make([]byte, 10)); err == nil {
		t.Error("ParseMinHash(10 bytes) err = nil, want ErrMinHashLength")
	}
}

func TestSimHashInt64RoundTrip(t *testing.T) {
	for _, h := range []SimHash{0, 1, 1 << 63, ^SimHash(0)} {
		if got := SimHashFromInt64(h.Int64()); got != h {
			t.Errorf("round trip of %x = %x", uint64(h), uint64(got))
		}
	}
}

func TestNewSimIndexRejectsBadDistance(t *testing.T) {
	for _, d := range []int{-1, MaxSimDistance + 1} {
		if _, err := NewSimIndex[int](d); err == nil {
			t.Errorf("NewSimIndex(%d) err = nil, want an error", d)
		}
	}
}

// Property: Near returns exactly the brute-force set within the distance, ordered.
func TestSimIndexMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, maxDist := range []int{0, 3, 7, 12} {
		ix, err := NewSimIndex[int](maxDist)
		if err != nil {
			t.Fatal(err)
		}
		stored := map[int]SimHash{}
		base := SimHash(rng.Uint64())
		for id := range 400 {
			h := base
			for range rng.IntN(16) { // flip up to 15 bits so many are near the base
				h ^= 1 << rng.IntN(64)
			}
			if rng.IntN(4) == 0 {
				h = SimHash(rng.Uint64())
			}
			stored[id] = h
			ix.Add(id, h)
		}
		for id := range 50 { // remove some to check removal keeps the index exact
			ix.Remove(id * 3)
			delete(stored, id*3)
		}
		for range 50 {
			q := base ^ (1 << rng.IntN(64))
			got := ix.Near(q, maxDist)
			want := 0
			for _, h := range stored {
				if Hamming(q, h) <= maxDist {
					want++
				}
			}
			if len(got) != want {
				t.Fatalf("maxDist %d: Near found %d, brute force %d (LSH banding must not miss matches)", maxDist, len(got), want)
			}
			for i, m := range got {
				if Hamming(q, stored[m.ID]) != m.Distance || m.Distance > maxDist {
					t.Fatalf("match %v has the wrong distance", m)
				}
				if i > 0 && got[i-1].Distance > m.Distance {
					t.Fatalf("matches not ordered by distance: %v", got)
				}
			}
		}
		if ix.Len() != len(stored) {
			t.Errorf("Len = %d, want %d", ix.Len(), len(stored))
		}
	}
}

func TestSimIndexReplaceAndQueryCap(t *testing.T) {
	ix, _ := NewSimIndex[string](3)
	ix.Add("a", 0)
	ix.Add("a", ^SimHash(0)) // replaces
	if got := ix.Near(0, 3); len(got) != 0 {
		t.Errorf("Near after replace = %v, want none (old hash must be gone)", got)
	}
	ix.Add("b", 0b1111111) // 7 bits from 0
	if got := ix.Near(0, 10); len(got) != 0 {
		t.Errorf("Near(0, 10) on a distance-3 index = %v, want none (query capped to the index distance)", got)
	}
	if got := ix.Near(0, -1); got != nil {
		t.Errorf("Near with negative distance = %v, want nil", got)
	}
}

func TestMinIndexSimilar(t *testing.T) {
	ix := NewMinIndex[int64]()
	texts := []string{
		campaign,
		strings.Replace(campaign, "500", "700", 1),
		"Are we still meeting at the library tomorrow afternoon to finish the chemistry homework?",
		"Join our VIP signals group for guaranteed forex profits every single day, link in bio",
	}
	for i, s := range texts {
		m, _ := ComputeMinHash(s)
		ix.Add(int64(i), m)
	}
	q, _ := ComputeMinHash(campaign + " today")
	got := ix.Similar(&q, 0.6)
	if len(got) != 2 || got[0].ID != 0 || got[1].ID != 1 {
		t.Fatalf("Similar = %v, want entries 0 then 1 (the campaign and its variant)", got)
	}
	ix.Remove(0)
	if got := ix.Similar(&q, 0.6); len(got) != 1 || got[0].ID != 1 {
		t.Errorf("Similar after Remove(0) = %v, want only entry 1", got)
	}
	if ix.Len() != 3 {
		t.Errorf("Len = %d, want 3", ix.Len())
	}
	if got := ix.Similar(&q, 0.99); len(got) != 0 {
		t.Errorf("Similar at 0.99 = %v, want none", got)
	}
}

func FuzzFingerprints(f *testing.F) {
	for _, s := range []string{campaign, "", "a", "اربح المال", "\xff\xfe", "🔥🔥 f r e e"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		h, ok := ComputeSimHash(s)
		m, okM := ComputeMinHash(s)
		if ok != okM {
			t.Fatalf("SimHash ok %v != MinHash ok %v", ok, okM)
		}
		if !ok {
			return
		}
		h2, _ := ComputeSimHash(Canonical(s))
		if h != h2 {
			t.Fatalf("SimHash changes after Canonical: %x vs %x", uint64(h), uint64(h2))
		}
		if j := Jaccard(&m, &m); j != 1 {
			t.Fatalf("self Jaccard %v", j)
		}
	})
}
