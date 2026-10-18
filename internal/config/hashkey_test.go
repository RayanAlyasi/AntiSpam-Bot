package config

import (
	"errors"
	"strings"
	"testing"
)

func TestNumberHashKey(t *testing.T) {
	tests := []struct {
		name, env string
		ok        bool
	}{
		{"missing", "", false},
		{"blank", "   ", false},
		{"too short", "0123456789abcde", false},
		{"minimum", "0123456789abcdef", true},
		{"hex 32", strings.Repeat("ab", 32), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			cfg, err := Load(func(k string) string {
				if k == "HASH_KEY" {
					return tt.env
				}
				return ""
			})
			if err != nil {
				t.Fatalf("Load: %v (a missing HASH_KEY must not stop config loading, only running the bot)", err)
			}
			key, err := cfg.NumberHashKey()
			if tt.ok {
				if err != nil || string(key) != strings.TrimSpace(tt.env) {
					t.Fatalf("NumberHashKey = %q, %v; want the key", key, err)
				}
				return
			}
			if !errors.Is(err, ErrHashKey) {
				t.Fatalf("NumberHashKey error %v, want ErrHashKey: the bot must refuse to start", err)
			}
			if strings.TrimSpace(tt.env) != "" && strings.Contains(err.Error(), strings.TrimSpace(tt.env)) {
				t.Fatalf("error %q leaks the key", err)
			}
		})
	}
}
