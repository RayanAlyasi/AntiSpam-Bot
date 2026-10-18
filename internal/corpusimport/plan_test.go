package corpusimport

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/neardup"
	"pgregory.net/rapid"
)

var testKey = []byte("corpusimport-test-number-key-01")

func opts() Options {
	return Options{Holdout: DefaultHoldout, HashKey: testKey, Now: time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)}
}

// vocab gives synthetic messages that share no template, so no two of them are near-duplicates.
var vocab = func() []string {
	var w []string
	for _, a := range []string{"ba", "ko", "mi", "tu", "re", "sa", "lo", "fe", "zu", "ni", "pa", "do", "ve", "gi", "hu"} {
		for _, b := range []string{"rk", "nt", "lm", "sp", "dr", "gh", "xt", "mp", "nd", "ck", "rt", "sk", "lv", "ft"} {
			w = append(w, a+b)
		}
	}
	return w
}()

// message returns the i-th synthetic message: ten words drawn with seed i.
func message(i int) string {
	r := rand.New(rand.NewPCG(uint64(i), 99))
	words := make([]string, 10)
	for j := range words {
		words[j] = vocab[r.IntN(len(vocab))]
	}
	return strings.Join(words, " ")
}

func spam(text, cluster string) Record {
	return Record{Line: 1, Text: text, Label: LabelSpam, ClusterID: cluster}
}

func TestNewPlanMergesDuplicates(t *testing.T) {
	base := message(1) + " " + message(2) + " join https://one.example/x"
	recs := []Record{
		spam(base, ""),
		spam(base, ""),                  // exact copy
		spam(strings.ToUpper(base), ""), // the same once normalized
		spam(base+" "+vocab[0]+".", ""), // a near copy
		spam(strings.Replace(base, "one.example", "two.example", 1), ""), // a near copy with another domain
		spam(message(3), ""),
	}
	p, err := NewPlan(recs, Options{HashKey: testKey, Now: opts().Now})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Corpus) != 2 || p.MergedInFile != 4 {
		t.Fatalf("corpus %d, merged %d; want 2 entries and 4 merged (copies and near copies are stored once, §7.8 step 3)", len(p.Corpus), p.MergedInFile)
	}
	if p.Corpus[0].Merged != 4 || len(p.Corpus[0].Entry.Domains) != 2 {
		t.Errorf("kept entry merged %d with domains %v, want 4 merged and both domains kept", p.Corpus[0].Merged, p.Corpus[0].Entry.Domains)
	}
	if p.Clusters != 2 {
		t.Errorf("clusters = %d, want 2 (near copies form one cluster)", p.Clusters)
	}
}

func TestNewPlanRedactsAndHashes(t *testing.T) {
	p, err := NewPlan([]Record{spam("Sick notes in one hour, call +966 55 123 4567 or mail a.b@example.com "+message(3), "")}, opts())
	if err != nil {
		t.Fatal(err)
	}
	e := p.Corpus[0].Entry
	for _, leak := range []string{"551234567", "55 123 4567", "a.b@example.com"} {
		if strings.Contains(e.TextRawRedacted, leak) || strings.Contains(e.TextNorm, leak) {
			t.Errorf("stored text keeps %q, want numbers hashed and emails masked (§7.5 retention)", leak)
		}
	}
	if len(e.NumberHashes) != 1 {
		t.Errorf("number hashes = %d, want the advertised number as one keyed hash", len(e.NumberHashes))
	}
	if e.Source != "import" || e.Status != "active" || e.Scope != "global" {
		t.Errorf("entry = source %s status %s scope %s, want an active global import entry", e.Source, e.Status, e.Scope)
	}
}

func TestNewPlanKeepsHamOutOfTheCorpus(t *testing.T) {
	recs := []Record{spam(message(4), ""), {Line: 2, Text: message(5), Label: LabelHam}}
	p, err := NewPlan(recs, opts())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Ham) != 1 || len(p.Corpus)+len(p.Holdout) != 1 {
		t.Fatalf("ham %d, spam %d+%d; want the ham record kept apart (never stored, §7.8)", len(p.Ham), len(p.Corpus), len(p.Holdout))
	}
	for _, it := range append(p.Corpus, p.Holdout...) {
		if it.Record.Label != LabelSpam {
			t.Errorf("a %s record is on the corpus side", it.Record.Label)
		}
	}
}

func TestSplitRatioAndSmallStrata(t *testing.T) {
	var recs []Record
	for i := range 50 { // one stratum: 50 clusters of 2
		recs = append(recs, spam(message(100+2*i), fmt.Sprint("c", i)), spam(message(101+2*i), fmt.Sprint("c", i)))
	}
	lone := spam(message(999), "lonely")
	lone.Category = "rare"
	recs = append(recs, lone)
	p, err := NewPlan(recs, opts())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Holdout) != 20 || p.HoldoutClusters != 10 {
		t.Errorf("hold-out %d records in %d clusters, want 20 in 10 (20%% of the stratum)", len(p.Holdout), p.HoldoutClusters)
	}
	for _, it := range p.Holdout {
		if it.Record.ClusterID == "lonely" {
			t.Error("a one-cluster stratum was held out, want it kept in the corpus (too small to split)")
		}
	}
	again, _ := NewPlan(recs, opts())
	for i := range p.Holdout {
		if p.Holdout[i].Cluster != again.Holdout[i].Cluster {
			t.Fatal("two runs split differently, want a fixed split so re-running an import is stable")
		}
	}
}

func TestSplitExplicitClusters(t *testing.T) {
	recs := []Record{spam(message(10), "a"), spam(message(11), "b"), spam(message(12), "a")}
	o := opts()
	o.HoldoutClusters = []string{"a"}
	p, err := NewPlan(recs, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Holdout) != 2 || len(p.Corpus) != 1 || p.Corpus[0].Record.ClusterID != "b" {
		t.Errorf("hold-out %d, corpus %d; want cluster a held out and b in the corpus", len(p.Holdout), len(p.Corpus))
	}
}

// TestSplitNeverLeaks is the §7.8/§18 invariant: no cluster, and no near-copy of a hold-out
// record, is on both sides of the split.
func TestSplitNeverLeaks(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 60).Draw(t, "n")
		var recs []Record
		for i := range n {
			text := message(rapid.IntRange(0, 25).Draw(t, "msg")) // repeats make duplicates across clusters
			if rapid.Bool().Draw(t, "variant") {
				text += " " + vocab[i%len(vocab)]
			}
			r := spam(text, rapid.SampledFrom([]string{"", "a", "b", "c", "d", "e"}).Draw(t, "cluster"))
			r.Line = i + 1
			r.Category = rapid.SampledFrom([]string{"", "jobs", "crypto"}).Draw(t, "category")
			recs = append(recs, r)
		}
		o := opts()
		o.Holdout = rapid.Float64Range(0, MaxHoldout).Draw(t, "holdout")
		p, err := NewPlan(recs, o)
		if err != nil {
			t.Fatal(err)
		}
		side := map[string]string{}
		for _, it := range p.Holdout {
			side[it.Cluster] = "holdout"
			if it.Record.ClusterID != "" {
				side["id:"+it.Record.ClusterID] = "holdout"
			}
		}
		for _, it := range p.Corpus {
			if side[it.Cluster] == "holdout" || (it.Record.ClusterID != "" && side["id:"+it.Record.ClusterID] == "holdout") {
				t.Fatalf("cluster %q (cluster_id %q) is on both sides of the split", it.Cluster, it.Record.ClusterID)
			}
			for _, h := range p.Holdout {
				if h.Entry.TextNorm == it.Entry.TextNorm {
					t.Fatalf("the same text is in the corpus and the hold-out: %q", it.Entry.TextNorm)
				}
				// Without a cluster_id on one side, even a loose near-copy must not cross.
				th := neardup.DefaultThresholds()
				loose := neardup.Distance(h.FP, it.FP) <= th.LooseSimHashMaxDistance || neardup.Jaccard(h.FP, it.FP) >= th.LooseMinHashMinJaccard
				if loose && (h.Record.ClusterID == "" || it.Record.ClusterID == "") {
					t.Fatalf("loose near-copies on both sides of the split: %q and %q", h.Entry.TextNorm, it.Entry.TextNorm)
				}
			}
		}
		if got := len(p.Corpus) + len(p.Holdout) + p.MergedInFile; got != n {
			t.Fatalf("%d records accounted for, want %d", got, n)
		}
	})
}

func TestDropExisting(t *testing.T) {
	recs := []Record{
		spam(message(20), ""), spam(message(21), ""),
		{Line: 3, MediaSHA256: []string{sha}, Label: LabelSpam},
		{Line: 4, PHash: "00000000000000ff", Label: LabelSpam},
		{Line: 5, PHash: "00000000000000fe", Label: LabelSpam},
	}
	p, err := NewPlan(recs, Options{HashKey: testKey, Now: opts().Now})
	if err != nil {
		t.Fatal(err)
	}
	n := p.DropExisting([]Existing{
		{TextNorm: p.Corpus[0].Entry.TextNorm + " " + vocab[3]}, {MediaSHA256: [][]byte{decodeHex(sha)}}, {PHashes: []int64{0xff}},
	})
	if n != 3 || len(p.Corpus) != 2 || p.MergedExisting != 3 {
		t.Errorf("dropped %d, corpus %d; want the near copy, the known media and the known pHash dropped (already stored)", n, len(p.Corpus))
	}
}

// TestSplitManySmallStrata: strata too small to split on their own still give the hold-out its
// share overall, none more than its own share rounded up.
func TestSplitManySmallStrata(t *testing.T) {
	var recs []Record
	for i := range 50 {
		for j := range 2 {
			r := spam(message(300+2*i+j), fmt.Sprint("c", i, "-", j))
			r.Category = fmt.Sprint("cat", i)
			recs = append(recs, r)
		}
	}
	p, err := NewPlan(recs, opts())
	if err != nil {
		t.Fatal(err)
	}
	if p.HoldoutClusters != 20 {
		t.Errorf("held out %d of 100 clusters, want 20 (the --holdout share overall)", p.HoldoutClusters)
	}
	per := map[string]int{}
	for _, it := range p.Holdout {
		per[it.Record.Category]++
	}
	for c, n := range per {
		if n > 1 {
			t.Errorf("category %s gave %d of its 2 clusters, want at most 1 (ceil of its share)", c, n)
		}
	}
}

func TestOptionsValidate(t *testing.T) {
	g, _ := ParseScope("group:120363000000000001@g.us")
	for name, o := range map[string]Options{
		"no key":              {Now: opts().Now},
		"holdout too large":   {HashKey: testKey, Holdout: 0.7, Now: opts().Now},
		"allow-ham, instance": {HashKey: testKey, AllowHam: true, Now: opts().Now},
	} {
		if err := o.Validate(); err == nil {
			t.Errorf("%s: no error, want the options refused", name)
		}
	}
	if err := (Options{HashKey: testKey, AllowHam: true, Scope: g, Now: opts().Now}).Validate(); err != nil {
		t.Errorf("allow-ham in a group: %v, want it accepted", err)
	}
}

func TestParseScope(t *testing.T) {
	for in, want := range map[string]string{
		"instance":                       "instance",
		"group:120363000000000001@g.us":  "group:120363000000000001@g.us",
		" group:120363000000000001@g.us": "group:120363000000000001@g.us",
		"group:":                         "",
		"group:123@s.whatsapp.net":       "",
		"global":                         "",
		"group:@g.us":                    "",
	} {
		s, err := ParseScope(in)
		if want == "" {
			if err == nil {
				t.Errorf("ParseScope(%q) = %s, want an error", in, s)
			}
			continue
		}
		if err != nil || s.String() != want {
			t.Errorf("ParseScope(%q) = %s, %v; want %s", in, s, err, want)
		}
	}
}

func TestDetectLanguage(t *testing.T) {
	for in, want := range map[string]string{
		"عذر طبي معتمد اليوم": "ar",
		"cheap followers now": "en",
		"дешёвые подписчики":  "ru",
		"12345 !!!":           "und",
		"日本語のテキスト":            "und",
	} {
		if got := DetectLanguage(in); got != want {
			t.Errorf("DetectLanguage(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestReport(t *testing.T) {
	recs := []Record{spam(message(30), ""), spam(message(30), ""), {Line: 3, Text: message(31), Label: LabelHam}}
	recs[0].Category = "promo"
	p, err := NewPlan(recs, opts())
	if err != nil {
		t.Fatal(err)
	}
	r := NewReport(p, []Rejection{{Line: 9, Reason: "label: bad"}}, true)
	if r.Read != 4 || r.Spam != 2 || r.Ham != 1 || r.Imported != 1 || r.MergedInFile != 1 {
		t.Errorf("report = %+v, want 4 read (2 spam, 1 ham, 1 rejected), 1 imported, 1 merged", r)
	}
	var b strings.Builder
	if err := r.Write(&b); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"dry run", "line 9: label: bad", "category promo (2)"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("report text lacks %q:\n%s", want, b.String())
		}
	}
}
