//go:build integration

package corpusimport_test

import (
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/corpusimport"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/corpusimport/pgimport"
)

func TestHamNeverStored(t *testing.T) {
	for _, allow := range []bool{false, true} {
		name := "without allow-ham"
		if allow {
			name = "with allow-ham"
		}
		t.Run(name, func(t *testing.T) {
			pool := newPool(t)
			g := newGen(20)
			recs := clustered(g, 10, 1)
			var hamTags []string
			for i := range 3 {
				hamTags = append(hamTags, tag("hx", i))
				recs = append(recs, ham(len(recs)+1, g.text(hamTags[i], 20)))
			}
			o := opts(inGroup(groupA), 0.2)
			o.AllowHam = allow
			rep := run(t, pool, recs, o)
			if rep.Spam != 10 || rep.Ham != 3 {
				t.Errorf("got spam %d ham %d, want 10 and 3", rep.Spam, rep.Ham)
			}
			for _, tg := range hamTags {
				if hits := textHits(t, pool, tg); len(hits) != 0 {
					t.Errorf("found ham text %s in %v, want it nowhere (ham records are never stored as text, DESIGN §7.8, §17)", tg, hits)
				}
			}
			n := count(t, pool, `SELECT count(*) FROM app.corpus_allow WHERE group_jid = $1`, string(groupA))
			if allow && (n < 1 || n > 3) {
				t.Errorf("got %d allow-list fingerprints, want 1..3 (with --allow-ham, ham adds fingerprint allow-list entries, DESIGN §7.8)", n)
			}
			if !allow && n != 0 {
				t.Errorf("got %d allow-list fingerprints, want 0 (without --allow-ham, ham is used in memory only, DESIGN §7.8)", n)
			}
		})
	}
}

func TestHamFalsePositiveCount(t *testing.T) {
	pool := newPool(t)
	g := newGen(21)
	w := g.words(tag("qx", 1), 100)
	unrelated := tag("hx", 1)
	recs := []corpusimport.Record{
		spam(1, strings.Join(w, " "), "a"),
		ham(2, g.edited(w)),
		ham(3, g.text(unrelated, 20)),
	}
	rep := run(t, pool, recs, opts(inGroup(groupA), 0))
	if rep.HamCaught != 1 {
		t.Errorf("got %d ham caught, want 1 (only the ham record that near-duplicates imported spam would be caught: the in-memory false-positive check, DESIGN §7.8)", rep.HamCaught)
	}
	if rep.Imported != 1 || rep.Ham != 2 {
		t.Errorf("got imported %d ham %d, want 1 and 2 (ham never merges into or replaces spam)", rep.Imported, rep.Ham)
	}
	if hits := textHits(t, pool, unrelated); len(hits) != 0 {
		t.Errorf("found ham text in %v, want it nowhere (DESIGN §7.8, §17)", hits)
	}
}

func TestRemoveUndoesBatch(t *testing.T) {
	pool := newPool(t)
	g := newGen(22)
	first := clustered(g, 5, 1)
	first[0].Text += " https://shared-promo.example/join"
	run(t, pool, first, opts(inGroup(groupA), 0))
	before := tableCounts(t, pool)

	var second []corpusimport.Record
	for i := range 10 {
		second = append(second, spam(i+1, g.text(tag("rx", i), 14), tag("bx", i)))
	}
	second[0].Text += " https://shared-promo.example/join"
	second[1].Text += " https://only-second.example/offer"
	second = append(second, first[2], ham(20, g.text(tag("hx", 1), 20)), ham(21, g.text(tag("hx", 2), 20)))
	o := opts(inGroup(groupA), 0.3)
	o.AllowHam = true
	o.HoldoutClusters = []string{tag("bx", 9)}
	rep := run(t, pool, second, o)
	if rep.MergedExisting != 1 || rep.Holdout < 1 {
		t.Fatalf("got merged-existing %d hold-out %d, want 1 and at least 1", rep.MergedExisting, rep.Holdout)
	}
	allow := count(t, pool, `SELECT count(*) FROM app.corpus_allow`) - before[`"app"."corpus_allow"`]

	rem, err := pgimport.Remove(t.Context(), pool, rep.BatchID)
	if err != nil {
		t.Fatalf("Remove: got error %v, want nil", err)
	}
	if rem.Entries != rep.Imported || rem.Holdout != rep.Holdout || rem.Allow != allow {
		t.Errorf("got removed %+v, want entries %d, hold-out %d, allow %d (remove --import deletes everything from the batch, DESIGN §7.8)", rem, rep.Imported, rep.Holdout, allow)
	}
	after := tableCounts(t, pool)
	for _, tbl := range []string{`"app"."corpus_entries"`, `"app"."eval_corpus"`, `"app"."corpus_allow"`, `"app"."rules"`, `"app"."learned_rules"`, `"app"."fingerprints"`, `"app"."corpus_confirmations"`} {
		if after[tbl] != before[tbl] {
			t.Errorf("table %s: got %d rows after remove, want %d as before the import (every entry, fingerprint and rule learned only from the batch goes; the earlier batch stays, DESIGN §7.8)", tbl, after[tbl], before[tbl])
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM app.corpus_entries WHERE text_norm LIKE '%' || $1 || '%'`, tag("qx", 2)); n != 1 {
		t.Errorf("got %d entries for the earlier batch's record the removed batch merged into, want 1 (removing a batch never touches another batch's entries)", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM app.import_batches WHERE id = $1`, rep.BatchID); n != 1 {
		t.Errorf("got %d import_batches rows for the removed batch, want 1 (the batch is marked removed, not forgotten)", n)
	}

	again, err := pgimport.Remove(t.Context(), pool, rep.BatchID)
	if err != nil || again != (pgimport.Removed{}) {
		t.Errorf("second Remove: got %+v, %v, want zero counts and nil (removing a batch twice is harmless)", again, err)
	}
	if _, err := pgimport.Remove(t.Context(), pool, rep.BatchID+1000); err == nil {
		t.Errorf("Remove of an unknown batch: got nil error, want one (a typo in --import must not look like success)")
	}
}
