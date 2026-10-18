package main

import (
	"bytes"
	"context"
	"flag"
	"slices"
	"strings"
	"testing"
)

func TestCorpusCmdBadCommandLines(t *testing.T) {
	env := map[string]string{"DATABASE_URL": "postgres://unused", "HASH_KEY": "corpus-cli-test-number-hash-key-0001"}
	getenv := func(k string) string { return env[k] }
	for name, args := range map[string][]string{
		"no subcommand":          {},
		"unknown subcommand":     {"export"},
		"no file":                {"import", "--scope", "instance"},
		"no scope":               {"import", "-"},
		"two files":              {"import", "a.jsonl", "b.jsonl", "--scope", "instance"},
		"bad scope":              {"import", "-", "--scope", "group:123@s.whatsapp.net"},
		"holdout too large":      {"import", "-", "--scope", "instance", "--holdout", "0.9"},
		"allow-ham for instance": {"import", "-", "--scope", "instance", "--allow-ham"},
		"remove without id":      {"remove"},
		"remove bad id":          {"remove", "--import", "abc"},
	} {
		var out, log bytes.Buffer
		if code := corpusCmd(context.Background(), args, getenv, strings.NewReader(""), &out, &log); code != 2 {
			t.Errorf("%s: exit %d, want 2 (a bad command line writes nothing)", name, code)
		}
	}
}

func TestParseInterspersed(t *testing.T) {
	for _, args := range [][]string{
		{"-", "--scope", "instance", "--dry-run"},
		{"--scope", "instance", "-", "--dry-run"},
		{"--dry-run", "--scope", "instance", "-"},
	} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		scope := fs.String("scope", "", "")
		dry := fs.Bool("dry-run", false, "")
		pos, err := parseInterspersed(fs, args)
		if err != nil || !slices.Equal(pos, []string{"-"}) || *scope != "instance" || !*dry {
			t.Errorf("%v: pos %v scope %q dry %v err %v; want the file and both flags whatever their order", args, pos, *scope, *dry, err)
		}
	}
}
