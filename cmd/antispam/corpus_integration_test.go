//go:build integration

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
)

// TestCorpusImportFromStdin runs the command the way the distroless image does (`-` reads stdin),
// then undoes the batch it printed.
func TestCorpusImportFromStdin(t *testing.T) {
	ctx := context.Background()
	url := storetest.URL(t, true)
	env := map[string]string{"DATABASE_URL": url, "HASH_KEY": "corpus-cli-test-number-hash-key-0001"}
	getenv := func(k string) string { return env[k] }
	in := strings.Join([]string{
		`{"text":"Cheap exam answers tonight, message the tutor service for the full pack","category":"cheating","cluster_id":"a"}`,
		`{"text":"Cheap exam answers tonight, message the tutor service for the full pack!","category":"cheating","cluster_id":"a"}`,
		`{"text":"Medical leave notes delivered in one hour from a licensed clinic","category":"medical","cluster_id":"b"}`,
		`{"text":"see you all at the meeting tomorrow","label":"ham"}`,
		`{"label":"spam"}`,
	}, "\n")

	var out, log bytes.Buffer
	if code := corpusCmd(ctx, []string{"import", "-", "--scope", "instance", "--dry-run"}, getenv, strings.NewReader(in), &out, &log); code != 0 {
		t.Fatalf("dry run exit %d; log: %s", code, log.String())
	}
	if !strings.Contains(out.String(), "dry run") || !strings.Contains(out.String(), "rejected line 5:") {
		t.Fatalf("dry-run report lacks the dry-run note or the rejected line:\n%s", out.String())
	}

	out.Reset()
	if code := corpusCmd(ctx, []string{"import", "-", "--scope", "instance", "--holdout", "0"}, getenv, strings.NewReader(in), &out, &log); code != 0 {
		t.Fatalf("import exit %d; log: %s", code, log.String())
	}
	m := regexp.MustCompile(`import batch (\d+)`).FindStringSubmatch(out.String())
	if m == nil || !strings.Contains(out.String(), "imported 2 entries; duplicates merged: 1 in the file") {
		t.Fatalf("report lacks the batch ID or the counts (2 imported, 1 merged):\n%s", out.String())
	}
	pool, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var entries int
	var name, format string
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM app.corpus_entries WHERE import_batch_id = $1`, m[1]).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT file_name, format FROM app.import_batches WHERE id = $1`, m[1]).Scan(&name, &format); err != nil {
		t.Fatal(err)
	}
	if entries != 2 || name != "stdin" || format != "jsonl" {
		t.Errorf("batch has %d entries, file %q, format %q; want 2 entries read from stdin as jsonl", entries, name, format)
	}

	out.Reset()
	if code := corpusCmd(ctx, []string{"remove", "--import", m[1]}, getenv, nil, &out, &log); code != 0 {
		t.Fatalf("remove exit %d; log: %s", code, log.String())
	}
	if !strings.Contains(out.String(), "2 corpus entries") {
		t.Errorf("remove report = %q, want 2 corpus entries removed", out.String())
	}
	if code := corpusCmd(ctx, []string{"remove", "--import", "999999"}, getenv, nil, &out, &log); code != 2 {
		t.Errorf("removing an unknown import: exit %d, want 2", code)
	}

	// A file argument and a CSV work the same way, with the flags after the file.
	path := filepath.Join(t.TempDir(), "spam.csv")
	if err := os.WriteFile(path, []byte("text,category\n\"Followers for sale, cheapest prices in town today only\",promo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := corpusCmd(ctx, []string{"import", path, "--scope", "group:120363000000000001@g.us"}, getenv, nil, &out, &log); code != 0 {
		t.Fatalf("CSV import exit %d; log: %s", code, log.String())
	}
	if !strings.Contains(out.String(), "imported 1 entries") {
		t.Errorf("CSV import report:\n%s\nwant 1 entry imported", out.String())
	}
}
