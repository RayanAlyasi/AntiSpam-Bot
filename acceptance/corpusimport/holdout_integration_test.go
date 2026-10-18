//go:build integration

package corpusimport_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/corpusimport"
)

// checkSplit fails if any of the tags is stored on both sides, and returns how many tags are on
// each side.
func checkSplit(t *testing.T, tags []string, both func(tag string) (bool, bool)) (train, hold int) {
	t.Helper()
	for _, tg := range tags {
		c, h := both(tg)
		if c && h {
			t.Errorf("cluster %s is both a corpus entry and a hold-out row, want one side only (the split is by cluster, so near-duplicates of a test record can't leak into the corpus, DESIGN §7.8, §18)", tg)
		}
		if c {
			train++
		}
		if h {
			hold++
		}
	}
	return train, hold
}

func TestHoldoutKeepsClustersTogether(t *testing.T) {
	pool := newPool(t)
	recs := clustered(newGen(10), 60, 3)
	rep := run(t, pool, recs, opts(instance, 0.3))

	var tags []string
	for i := range 60 {
		tags = append(tags, tag("qx", i))
	}
	train, hold := checkSplit(t, tags, func(tg string) (bool, bool) { return side(t, pool, rep.BatchID, tg) })
	if train == 0 || hold == 0 {
		t.Fatalf("got %d clusters in the corpus and %d in the hold-out, want both non-zero with --holdout 0.3", train, hold)
	}
	if rep.Clusters != 60 || rep.HoldoutClusters != hold || rep.Imported+rep.Holdout != 180 {
		t.Errorf("got clusters %d hold-out clusters %d imported %d hold-out %d, want 60, %d, and imported + hold-out = 180", rep.Clusters, rep.HoldoutClusters, rep.Imported, rep.Holdout, hold)
	}
	if n := count(t, pool, `SELECT count(*) FROM app.eval_corpus WHERE import_batch_id = $1 AND source = 'import_holdout' AND label = 'spam'`, rep.BatchID); n != rep.Holdout {
		t.Errorf("got %d import_holdout spam rows in eval_corpus, want %d (the hold-out is stored in eval_corpus, DESIGN §14)", n, rep.Holdout)
	}
	if n := count(t, pool, `SELECT count(DISTINCT cluster_key) FROM app.eval_corpus WHERE import_batch_id = $1`, rep.BatchID); n != hold {
		t.Errorf("got %d distinct cluster keys in the hold-out, want %d (records of one cluster share a cluster key)", n, hold)
	}
	if n := count(t, pool, `SELECT count(*) FROM app.corpus_entries WHERE import_batch_id = $1`, rep.BatchID); n != rep.Imported {
		t.Errorf("got %d corpus entries, want %d (hold-out records are never corpus entries, DESIGN §18)", n, rep.Imported)
	}
}

func TestHoldoutComputedClusters(t *testing.T) {
	pool := newPool(t)
	g := newGen(11)
	var recs []corpusimport.Record
	var tags []string
	for i := range 30 {
		tg := tag("qx", i)
		tags = append(tags, tg)
		w := g.words(tg, 100)
		for _, text := range []string{strings.Join(w, " "), obfuscated(w), g.edited(w), g.rewritten(w)} {
			recs = append(recs, spam(len(recs)+1, text, ""))
		}
	}
	rep := run(t, pool, recs, opts(instance, 0.3))

	train, hold := checkSplit(t, tags, func(tg string) (bool, bool) { return side(t, pool, rep.BatchID, tg) })
	if train == 0 || hold == 0 {
		t.Fatalf("got %d templates in the corpus and %d in the hold-out, want both non-zero with --holdout 0.3", train, hold)
	}
	if rep.Clusters != 30 || rep.MergedInFile != 60 || rep.Imported+rep.Holdout != 60 {
		t.Errorf("got clusters %d merged in file %d imported+hold-out %d, want 30, 60, 60 (without cluster_id, near-duplicates form one cluster; exact and tight ones are merged, DESIGN §7.8)", rep.Clusters, rep.MergedInFile, rep.Imported+rep.Holdout)
	}
}

func TestHoldoutRatio(t *testing.T) {
	tests := []struct {
		holdout  float64
		min, max int
	}{
		{0, 0, 0},
		{0.2, 6, 14},
		{0.5, 19, 31},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.holdout), func(t *testing.T) {
			pool := newPool(t)
			rep := run(t, pool, clustered(newGen(12), 50, 2), opts(instance, tt.holdout))
			if rep.HoldoutClusters < tt.min || rep.HoldoutClusters > tt.max {
				t.Errorf("got %d of 50 clusters held out with --holdout %v, want %d..%d (the hold-out is about that share of clusters, DESIGN §7.8)", rep.HoldoutClusters, tt.holdout, tt.min, tt.max)
			}
			if n := count(t, pool, `SELECT count(*) FROM app.eval_corpus WHERE import_batch_id = $1`, rep.BatchID); n != rep.Holdout || n != 2*rep.HoldoutClusters {
				t.Errorf("got %d eval_corpus rows, want hold-out %d = 2 x %d clusters", n, rep.Holdout, rep.HoldoutClusters)
			}
		})
	}
}

func TestHoldoutStratified(t *testing.T) {
	pool := newPool(t)
	recs := clustered(newGen(13), 50, 1)
	for i := 40; i < 50; i++ {
		recs[i].Category, recs[i].Kind = "jobs", "link"
		recs[i].URLs = []string{"https://" + tag("jx", i) + ".example/apply"}
	}
	rep := run(t, pool, recs, opts(instance, 0.2))
	for _, q := range []struct{ col, val string }{{"category", "crypto"}, {"category", "jobs"}, {"kind", "text"}, {"kind", "link"}} {
		if n := count(t, pool, `SELECT count(*) FROM app.eval_corpus WHERE import_batch_id = $1 AND `+q.col+` = $2`, rep.BatchID, q.val); n == 0 {
			t.Errorf("got no hold-out rows with %s %s, want some (the hold-out is stratified by category and kind, DESIGN §7.8)", q.col, q.val)
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM app.eval_corpus WHERE import_batch_id = $1 AND category = 'jobs'`, rep.BatchID); n > 4 {
		t.Errorf("got %d of 10 jobs records held out with --holdout 0.2, want at most 4 (stratified, not over-sampled)", n)
	}
}

func TestHoldoutForcedClusters(t *testing.T) {
	pool := newPool(t)
	o := opts(instance, 0)
	o.HoldoutClusters = []string{"c03", "c11"}
	rep := run(t, pool, clustered(newGen(14), 20, 2), o)
	for _, i := range []int{3, 11} {
		if c, h := side(t, pool, rep.BatchID, tag("qx", i)); c || !h {
			t.Errorf("cluster c%02d: got corpus=%v hold-out=%v, want hold-out only (--holdout-clusters pins those clusters to the hold-out)", i, c, h)
		}
	}
	if rep.HoldoutClusters != 2 || rep.Holdout != 4 {
		t.Errorf("got %d hold-out clusters, %d records, want 2 and 4", rep.HoldoutClusters, rep.Holdout)
	}
}

func TestReimportNeverLeaksHoldout(t *testing.T) {
	pool := newPool(t)
	recs := clustered(newGen(15), 10, 2)
	o := opts(instance, 0)
	o.HoldoutClusters = []string{"c03"}
	run(t, pool, recs, o)
	run(t, pool, recs, opts(instance, 0))
	if n := count(t, pool, `SELECT count(*) FROM app.corpus_entries WHERE text_norm LIKE '%' || $1 || '%'`, tag("qx", 3)); n != 0 {
		t.Errorf("got %d corpus entries for a held-out cluster after re-importing the file, want 0 (the hold-out is never used for learning, DESIGN §18)", n)
	}
}

func TestHoldoutRecall(t *testing.T) {
	pool := newPool(t)
	g := newGen(16)
	w := g.words(tag("qx", 1), 100)
	recs := []corpusimport.Record{
		spam(1, strings.Join(w, " "), "tr"),
		spam(2, g.rewritten(w), "hl"),
		spam(3, g.text(tag("qx", 2), 30), "hm"),
	}
	o := opts(inGroup(groupA), 0)
	o.HoldoutClusters = []string{"hl", "hm"}
	rep := run(t, pool, recs, o)
	r := rep.Recall
	if rep.Holdout != 2 || r.Total != 2 || r.Deleted+r.ToAdmins+r.Missed != r.Total {
		t.Fatalf("got hold-out %d recall %+v, want total 2 = deleted + to admins + missed (the hold-out runs through the pipeline, DESIGN §18)", rep.Holdout, r)
	}
	if r.Deleted+r.ToAdmins < 1 {
		t.Errorf("got recall %+v, want the loose rewrite of a stored template caught (at least sent to admins, DESIGN §7.0, §18)", r)
	}
	if r.Missed < 1 {
		t.Errorf("got recall %+v, want the unrelated record missed (nothing in the corpus resembles it)", r)
	}
	for _, tbl := range []string{"app.detections", "app.actions"} {
		if n := count(t, pool, "SELECT count(*) FROM "+tbl); n != 0 {
			t.Errorf("got %d rows in %s, want 0 (the recall check runs in memory: nothing is sent or recorded)", n, tbl)
		}
	}
}
