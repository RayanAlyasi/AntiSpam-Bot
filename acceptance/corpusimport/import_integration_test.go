//go:build integration

package corpusimport_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/corpusimport"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/corpusimport/pgimport"
)

func TestImportStoresEntriesInScope(t *testing.T) {
	tests := []struct {
		name      string
		scope     corpusimport.Scope
		batchCond string
		entryCond string
	}{
		{"instance", instance, "scope = 'instance' AND group_jid IS NULL", "scope = 'global' AND group_jid IS NULL"},
		{"group", inGroup(groupA), "scope = 'group' AND group_jid = '" + string(groupA) + "'", "scope = 'group' AND group_jid = '" + string(groupA) + "'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := newPool(t)
			recs := clustered(newGen(1), 5, 1)
			rejected := []corpusimport.Rejection{{Line: 7, Reason: "text is empty"}}
			rep, err := pgimport.Run(t.Context(), pool, recs, rejected, opts(tt.scope, 0))
			if err != nil {
				t.Fatalf("Run: got error %v, want nil", err)
			}
			if rep.BatchID <= 0 || rep.DryRun {
				t.Errorf("got batch %d dry-run=%v, want a stored batch id (every entry keeps its import-batch ID so the import can be undone, DESIGN §7.8)", rep.BatchID, rep.DryRun)
			}
			if rep.Read != 6 || rep.Spam != 5 || rep.Ham != 0 || rep.Imported != 5 || rep.MergedInFile != 0 || rep.MergedExisting != 0 || rep.Holdout != 0 {
				t.Errorf("got report %+v, want read 6 (5 records + 1 rejected), spam 5, imported 5, nothing merged or held out", rep)
			}
			if !slices.Equal(rep.Rejected, rejected) {
				t.Errorf("got rejected %+v, want %+v (the report lists rejected lines, DESIGN §7.8)", rep.Rejected, rejected)
			}
			id := rep.BatchID
			if n := count(t, pool, `SELECT count(*) FROM app.import_batches WHERE id = $1 AND file_name = 'synthetic.jsonl' AND `+tt.batchCond, id); n != 1 {
				t.Errorf("got %d import_batches rows for batch %d with file name and scope %s, want 1 (DESIGN §14 import_batches)", n, id, tt.name)
			}
			if n := count(t, pool, `SELECT count(*) FROM app.corpus_entries WHERE import_batch_id = $1`, id); n != 5 {
				t.Errorf("got %d corpus entries for the batch, want 5", n)
			}
			if n := count(t, pool, `SELECT count(*) FROM app.corpus_entries WHERE import_batch_id = $1 AND source = 'import' AND status = 'active' AND label = 'spam' AND kind = 'message' AND `+tt.entryCond, id); n != 5 {
				t.Errorf("got %d entries with source import, status active, label spam and scope %s, want 5 (imports are stored in the scope the operator picks, DESIGN §7.8)", n, tt.name)
			}
			if n := count(t, pool, `SELECT count(*) FROM app.corpus_entries WHERE import_batch_id = $1 AND (simhash IS NULL OR minhash IS NULL OR text_norm = '')`, id); n != 0 {
				t.Errorf("got %d entries without normalized text, SimHash or MinHash, want 0 (imported spam is fingerprinted like confirmed spam, DESIGN §7.5, §7.8)", n)
			}
		})
	}
}

func TestImportRedactsAndHashesNumbers(t *testing.T) {
	pool := newPool(t)
	g := newGen(2)
	trainTag, holdTag := tag("qx", 1), tag("qx", 2)
	recs := []corpusimport.Record{
		spam(1, trainTag+" call +966501234567 or write to promoter@example.com for the bonus "+g.text("", 12), "train"),
		spam(2, holdTag+" message +966507654321 today for crypto signals "+g.text("", 12), "hold"),
	}
	o := opts(inGroup(groupA), 0)
	o.HoldoutClusters = []string{"hold"}
	rep := run(t, pool, recs, o)
	if rep.Imported != 1 || rep.Holdout != 1 {
		t.Fatalf("got imported %d hold-out %d, want 1 and 1", rep.Imported, rep.Holdout)
	}
	for _, needle := range []string{"501234567", "507654321", "promoter@example.com"} {
		if hits := textHits(t, pool, needle); len(hits) != 0 {
			t.Errorf("found %q in %v, want it nowhere (stored text is redacted and numbers are kept only as keyed hashes, DESIGN §7.5, §7.8, §17)", needle, hits)
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM app.corpus_entries WHERE import_batch_id = $1 AND text_raw_redacted LIKE '%' || $2 || '%' AND cardinality(number_hashes) > 0`, rep.BatchID, trainTag); n != 1 {
		t.Errorf("got %d entries keeping the redacted text and a number hash, want 1 (the advertised number is matchable as a keyed hash, DESIGN §7.5)", n)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	pool := newPool(t)
	g := newGen(3)
	existing := clustered(g, 3, 1)
	run(t, pool, existing, opts(inGroup(groupA), 0))

	recs := append(clustered(newGen(33), 12, 2), existing[0], ham(100, g.text(tag("hx", 1), 10)))
	before := tableCounts(t, pool)

	o := opts(inGroup(groupA), 0)
	o.DryRun = true
	dry := run(t, pool, recs, o)
	sameCounts(t, before, tableCounts(t, pool), "--dry-run writes nothing, DESIGN §7.8")
	if !dry.DryRun || dry.BatchID != 0 {
		t.Errorf("got dry-run=%v batch %d, want true and 0 (a dry run creates no batch)", dry.DryRun, dry.BatchID)
	}

	o2 := opts(inGroup(groupA), 0.3)
	o2.DryRun, o2.AllowHam, o2.HoldoutClusters = true, true, []string{"c01"}
	run(t, pool, recs, o2)
	sameCounts(t, before, tableCounts(t, pool), "--dry-run writes nothing, not even hold-out rows or ham allow-list fingerprints, DESIGN §7.8")

	real := run(t, pool, recs, opts(inGroup(groupA), 0))
	if dry.Read != real.Read || dry.Spam != real.Spam || dry.Ham != real.Ham || dry.Imported != real.Imported ||
		dry.MergedInFile != real.MergedInFile || dry.MergedExisting != real.MergedExisting || dry.Clusters != real.Clusters {
		t.Errorf("got dry-run report %+v and real report %+v, want the same counts (a dry run shows what would happen, DESIGN §7.8)", dry, real)
	}
	if real.MergedExisting != 1 || real.Imported != 24 {
		t.Errorf("got imported %d merged-existing %d, want 24 and 1", real.Imported, real.MergedExisting)
	}
}

func TestDedupeCounts(t *testing.T) {
	pool := newPool(t)
	g := newGen(4)
	d := g.words(tag("qx", 4), 100)
	run(t, pool, []corpusimport.Record{spam(1, strings.Join(d, " "), "d")}, opts(inGroup(groupA), 0))

	a := g.words(tag("qx", 1), 100)
	recs := []corpusimport.Record{
		spam(1, strings.Join(a, " "), "a"),
		spam(2, obfuscated(a), "a"),
		spam(3, g.edited(a), "a"),
		spam(4, g.text(tag("qx", 2), 100), "b"),
		spam(5, g.text(tag("qx", 3), 100), "c"),
		spam(6, g.edited(d), "d"),
	}
	rep := run(t, pool, recs, opts(inGroup(groupA), 0))
	if rep.Read != 6 || rep.Spam != 6 || rep.Imported != 3 || rep.MergedInFile != 2 || rep.MergedExisting != 1 {
		t.Errorf("got read %d spam %d imported %d merged in file %d merged existing %d, want 6, 6, 3, 2, 1 (exact, obfuscated and tight near-duplicates are merged and counted, not stored twice, DESIGN §7.8)",
			rep.Read, rep.Spam, rep.Imported, rep.MergedInFile, rep.MergedExisting)
	}
	if n := count(t, pool, `SELECT count(*) FROM app.corpus_entries WHERE import_batch_id = $1`, rep.BatchID); n != 3 {
		t.Errorf("got %d entries for the batch, want 3", n)
	}
	for _, tg := range []string{tag("qx", 1), tag("qx", 4)} {
		if n := count(t, pool, `SELECT count(*) FROM app.corpus_entries WHERE text_norm LIKE '%' || $1 || '%'`, tg); n != 1 {
			t.Errorf("got %d entries for template %s, want 1 (duplicates are merged, DESIGN §7.8)", n, tg)
		}
	}
}

func TestDedupeOnlyWithinScope(t *testing.T) {
	pool := newPool(t)
	recs := []corpusimport.Record{spam(1, newGen(5).text(tag("qx", 1), 30), "a")}
	steps := []struct {
		name               string
		scope              corpusimport.Scope
		imported, existing int
	}{
		{"first import in group A", inGroup(groupA), 1, 0},
		{"same text in group B", inGroup(groupB), 1, 0},
		{"same text instance-wide", instance, 1, 0},
		{"re-import in group A", inGroup(groupA), 0, 1},
		{"re-import instance-wide", instance, 0, 1},
	}
	for _, s := range steps {
		rep := run(t, pool, recs, opts(s.scope, 0))
		if rep.Imported != s.imported || rep.MergedExisting != s.existing {
			t.Errorf("%s: got imported %d merged-existing %d, want %d and %d (dedupe is against the live corpus in the same scope; re-importing a file stores nothing twice)",
				s.name, rep.Imported, rep.MergedExisting, s.imported, s.existing)
		}
	}
}

func TestThinCategoriesAndLanguages(t *testing.T) {
	pool := newPool(t)
	g := newGen(6)
	var recs []corpusimport.Record
	for i := range 27 {
		r := spam(i+1, g.text(tag("qx", i), 14), "")
		switch {
		case i < 3:
			r.Category = "jobs"
		case i < 5:
			r.Language = "fr"
		}
		recs = append(recs, r)
	}
	o := opts(instance, 0)
	o.DryRun = true
	rep := run(t, pool, recs, o)
	has := func(s string) bool {
		return slices.ContainsFunc(rep.Thin, func(x string) bool { return strings.Contains(x, s) })
	}
	if !has("jobs") || !has("fr") {
		t.Errorf("got thin %q, want it to name category jobs (3 examples) and language fr (2 examples) (the report lists categories or languages with too few examples, DESIGN §7.8)", rep.Thin)
	}
	if has("crypto") {
		t.Errorf("got thin %q, want crypto absent (24 examples is enough)", rep.Thin)
	}
}
