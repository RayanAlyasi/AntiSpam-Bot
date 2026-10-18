//go:build integration

package corpusimport_test

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/corpusimport"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/corpusimport/pgimport"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

var (
	t0      = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	hashKey = []byte("acceptance-corpus-import-key-0001")
)

const (
	groupA wa.JID = "120363000000000001@g.us"
	groupB wa.JID = "120363000000000002@g.us"
)

var instance = corpusimport.Scope{}

func inGroup(g wa.JID) corpusimport.Scope { return corpusimport.Scope{Group: g} }

func newPool(t *testing.T) *pgxpool.Pool { t.Helper(); return storetest.Pool(t) }

func opts(scope corpusimport.Scope, holdout float64) corpusimport.Options {
	return corpusimport.Options{Scope: scope, Holdout: holdout, HashKey: hashKey, FileName: "synthetic.jsonl", Now: t0}
}

func run(t *testing.T, pool *pgxpool.Pool, recs []corpusimport.Record, o corpusimport.Options) corpusimport.Report {
	t.Helper()
	rep, err := pgimport.Run(t.Context(), pool, recs, nil, o)
	if err != nil {
		t.Fatalf("Run: got error %v, want nil", err)
	}
	return rep
}

func count(t *testing.T, pool *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int64
	if err := pool.QueryRow(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return int(n)
}

// side reports whether text tagged with tag is stored as a corpus entry and as a hold-out row
// of batch.
func side(t *testing.T, pool *pgxpool.Pool, batch int64, tag string) (corpus, holdout bool) {
	t.Helper()
	c := count(t, pool, `SELECT count(*) FROM app.corpus_entries WHERE import_batch_id = $1 AND text_norm LIKE '%' || $2 || '%'`, batch, tag)
	h := count(t, pool, `SELECT count(*) FROM app.eval_corpus WHERE import_batch_id = $1 AND text_norm LIKE '%' || $2 || '%'`, batch, tag)
	return c > 0, h > 0
}

// tableCounts returns the row count of every table in the database.
func tableCounts(t *testing.T, pool *pgxpool.Pool) map[string]int {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT table_schema, table_name FROM information_schema.tables
		WHERE table_type = 'BASE TABLE' AND table_schema NOT IN ('pg_catalog', 'information_schema')`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var ids []pgx.Identifier
	for rows.Next() {
		var s, n string
		if err := rows.Scan(&s, &n); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		ids = append(ids, pgx.Identifier{s, n})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list tables: %v", err)
	}
	out := map[string]int{}
	for _, id := range ids {
		out[id.Sanitize()] = count(t, pool, "SELECT count(*) FROM "+id.Sanitize())
	}
	return out
}

func sameCounts(t *testing.T, before, after map[string]int, why string) {
	t.Helper()
	for k, a := range after {
		if b := before[k]; a != b {
			t.Errorf("table %s: got %d rows, want %d (%s)", k, a, b, why)
		}
	}
}

// textHits lists every text-like column, in any table, that contains needle.
func textHits(t *testing.T, pool *pgxpool.Pool, needle string) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT c.table_schema, c.table_name, c.column_name, c.data_type
		FROM information_schema.columns c
		JOIN information_schema.tables tb ON tb.table_schema = c.table_schema AND tb.table_name = c.table_name
		WHERE tb.table_type = 'BASE TABLE'
		  AND c.table_schema NOT IN ('pg_catalog', 'information_schema')
		  AND (c.data_type IN ('text', 'character varying', 'json', 'jsonb', 'bytea')
		       OR (c.data_type = 'ARRAY' AND c.udt_name IN ('_text', '_varchar')))`)
	if err != nil {
		t.Fatalf("list columns: %v", err)
	}
	type col struct{ schema, table, name, typ string }
	var cols []col
	for rows.Next() {
		var c col
		if err := rows.Scan(&c.schema, &c.table, &c.name, &c.typ); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list columns: %v", err)
	}
	var hits []string
	for _, c := range cols {
		cn := pgx.Identifier{c.name}.Sanitize()
		cond := cn + "::text ILIKE '%' || $1 || '%'"
		if c.typ == "bytea" {
			cond = "position(convert_to($1, 'UTF8') in " + cn + ") > 0"
		}
		if count(t, pool, "SELECT count(*) FROM "+pgx.Identifier{c.schema, c.table}.Sanitize()+" WHERE "+cond, needle) > 0 {
			hits = append(hits, c.schema+"."+c.table+"."+c.name)
		}
	}
	return hits
}

// ---- synthetic records ----

// Consonant-vowel syllables: words built from them never have doubled letters, so
// normalization (repeat folding, leetspeak) leaves them as they are.
var syllables = []string{"ba", "ke", "mi", "no", "tu", "ra", "se", "li", "po", "du", "ga", "fe", "zi", "vo", "ha", "je", "wu", "ky"}

// tag is a unique, normalization-stable marker word for cluster or record i.
func tag(prefix string, i int) string {
	n := len(syllables)
	return prefix + syllables[i%n] + syllables[(i/n)%n] + syllables[(i/(n*n))%n]
}

type gen struct{ rng *rand.Rand }

func newGen(seed uint64) *gen { return &gen{rng: rand.New(rand.NewPCG(seed, 99))} }

func (g *gen) word() string {
	var b strings.Builder
	for range 3 {
		b.WriteString(syllables[g.rng.IntN(len(syllables))])
	}
	return b.String()
}

// words returns tag followed by n random words.
func (g *gen) words(tag string, n int) []string {
	w := []string{tag}
	for range n {
		w = append(w, g.word())
	}
	return w
}

func (g *gen) text(tag string, n int) string { return strings.Join(g.words(tag, n), " ") }

// edited changes the last word: a tight near-duplicate of a long text (neardup Certain band).
func (g *gen) edited(w []string) string {
	c := append([]string(nil), w...)
	for nw := g.word(); ; nw = g.word() {
		if nw != c[len(c)-1] {
			c[len(c)-1] = nw
			break
		}
	}
	return strings.Join(c, " ")
}

// rewritten replaces the last quarter of the words: a loose near-duplicate.
func (g *gen) rewritten(w []string) string {
	c := append([]string(nil), w...)
	for i := len(c) * 3 / 4; i < len(c); i++ {
		c[i] = g.word()
	}
	return strings.Join(c, " ")
}

// obfuscated is the same text with zero-width characters inside words and one word in
// fullwidth letters: identical once normalized (DESIGN §7.2). The tag stays plain.
func obfuscated(w []string) string {
	c := append([]string(nil), w...)
	for i := 1; i < len(c); i += 5 {
		c[i] = c[i][:1] + "​" + c[i][1:]
	}
	var fw strings.Builder
	for _, r := range c[2] {
		if r >= 'a' && r <= 'z' {
			r += 0xFF41 - 'a'
		}
		fw.WriteRune(r)
	}
	c[2] = fw.String()
	return strings.Join(c, " ")
}

func spam(line int, text, cluster string) corpusimport.Record {
	return corpusimport.Record{Line: line, Text: text, Label: "spam", Kind: "text", Language: "en", Category: "crypto", ClusterID: cluster}
}

func ham(line int, text string) corpusimport.Record {
	return corpusimport.Record{Line: line, Text: text, Label: "ham", Language: "en"}
}

// clustered returns nClusters clusters of perCluster distinct records each. Cluster i has
// cluster_id "c<i>" and every record carries tag("qx", i).
func clustered(g *gen, nClusters, perCluster int) []corpusimport.Record {
	var recs []corpusimport.Record
	for i := range nClusters {
		for range perCluster {
			recs = append(recs, spam(len(recs)+1, g.text(tag("qx", i), 14), fmt.Sprintf("c%02d", i)))
		}
	}
	return recs
}
