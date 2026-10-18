package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fixture = "../../internal/replay/testdata/v01.jsonl"

func TestReplayCommandLine(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
	}{
		{"neither source", nil, 2},
		{"both sources", []string{"-fixtures", fixture, "-shadow"}, 2},
		{"stray argument", []string{"-fixtures", fixture, "extra"}, 2},
		{"bad since", []string{"-shadow", "-since", "-1h"}, 2},
		{"bad days", []string{"-shadow", "-since", "0d"}, 2},
		{"compare without fixtures", []string{"-inbox", "-compare", "w.yaml"}, 2},
		{"shadow without a database", []string{"-shadow"}, 1},
		{"missing fixture", []string{"-fixtures", "nope.jsonl"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, log bytes.Buffer
			if got := replayCmd(t.Context(), tt.args, func(string) string { return "" }, &out, &log); got != tt.code {
				t.Fatalf("replayCmd(%v) = %d, want %d; log: %s", tt.args, got, tt.code, log.String())
			}
		})
	}
}

func TestReplayFixturesAndCompare(t *testing.T) {
	dir := t.TempDir()
	// A weight set that scores the corpus match as nothing: every confirmed template is let through.
	off := filepath.Join(dir, "off.yaml")
	if err := os.WriteFile(off, []byte("version: \"off\"\nbias: -3.0\nsignals:\n  corpus.near_duplicate: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, log bytes.Buffer
	if code := replayCmd(t.Context(), []string{"-fixtures", fixture, "-compare", off}, func(string) string { return "" }, &out, &log); code != 0 {
		t.Fatalf("exit %d; log: %s", code, log.String())
	}
	got := out.String()
	for _, want := range []string{
		"S1 mode=enforce band=certain source=known route=auto_ban actions=delete+ban expect=spam",
		"spam: 5 deleted, 0 to admins, 0 missed (of 5)",
		"-- ",
		"changed with " + off,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "-- 0 changed") {
		t.Errorf("zeroing the corpus weight must change verdicts:\n%s", got)
	}
}

func TestParseSince(t *testing.T) {
	for in, want := range map[string]time.Duration{"7d": 7 * 24 * time.Hour, "1d": 24 * time.Hour, "36h": 36 * time.Hour, "90m": 90 * time.Minute} {
		if got, err := parseSince(in); err != nil || got != want {
			t.Errorf("parseSince(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"d", "-2d", "x", "1.5d"} {
		if _, err := parseSince(in); err == nil {
			t.Errorf("parseSince(%q) succeeded, want an error", in)
		}
	}
}
