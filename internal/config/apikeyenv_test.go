package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPIKeyPastedIntoAPIKeyEnvIsNeverEchoed(t *testing.T) {
	keys := []string{
		"AIzaSyA1b2C3d4E5f6G7h8I9j0KlMnOpQrStUvW", // Google-style, no "-"
		"sk-ant-api03-abcDEF123",                  // Anthropic-style
		"abc_def_123",                             // lower-case, would be a legal shell name
	}
	for _, key := range keys {
		t.Run(key[:6], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			body := "ai:\n  provider: google\n  model: gemini\n  api_key_env: " + key + "\n"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(func(k string) string {
				if k == "CONFIG_FILE" {
					return path
				}
				return ""
			})
			if err == nil {
				t.Fatal("Load accepted a key in ai.api_key_env, want an error")
			}
			if strings.Contains(err.Error(), key) {
				t.Errorf("error %q echoes the value of ai.api_key_env (it may be a pasted API key)", err)
			}
			if !strings.Contains(err.Error(), "ai.api_key_env") {
				t.Errorf("error %q doesn't name ai.api_key_env", err)
			}
		})
	}
}
