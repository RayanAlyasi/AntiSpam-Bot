//go:build integration

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
)

// TestServeRefusesPairedBotWithoutHashKey: once a number is paired, moderation needs HASH_KEY,
// so the bot refuses to start without a valid one, and starts with one.
func TestServeRefusesPairedBotWithoutHashKey(t *testing.T) {
	url := storetest.URL(t, true)
	pool, err := store.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.NewBots(pool).UpsertBot(context.Background(), "966500000009@s.whatsapp.net", "100@lid", "bot", "active"); err != nil {
		t.Fatal(err)
	}
	pool.Close()

	for _, tt := range []struct {
		name, key string
		code      int
		log       string
	}{
		{"missing", "", 1, "refusing to start"},
		{"too short", "Zq9-x", 1, "too short"},
		{"valid", "0123456789abcdef0123456789abcdef", 0, "schema up to date"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{"DATABASE_URL": url, "HTTP_ADDR": freeAddr(t), "HASH_KEY": tt.key}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var out lockedBuffer
			if code := cli(ctx, []string{"-roles", "workers"}, func(k string) string { return env[k] }, &out); code != tt.code {
				t.Fatalf("exit code %d, want %d; log:\n%s", code, tt.code, out.String())
			}
			if !strings.Contains(out.String(), tt.log) {
				t.Fatalf("log lacks %q:\n%s", tt.log, out.String())
			}
			if tt.key != "" && strings.Contains(out.String(), tt.key) {
				t.Fatalf("the log leaks HASH_KEY:\n%s", out.String())
			}
		})
	}
}
