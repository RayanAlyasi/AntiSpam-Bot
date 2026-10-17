package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// loadWith writes file (when non-empty) to a temp config.yaml and loads it with env.
func loadWith(t *testing.T, file string, env map[string]string) (Config, error) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	if file != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Load(func(k string) string { return env[k] })
}

func TestLoadFile(t *testing.T) {
	tests := []struct {
		name  string
		file  string
		env   map[string]string
		check func(Config) bool
	}{
		{
			"null leaf keeps its default", "pacing:\n  burst:\n", nil,
			func(c Config) bool { return c.Pacing.Burst == 10 },
		},
		{
			"jitter may be zero, written as 0", "pacing:\n  spacing_jitter: 0\n", nil,
			func(c Config) bool { return c.Pacing.SpacingJitter == 0 },
		},
		{
			"integer accepted for a float", "scoring:\n  thresholds: {ban: 1}\n", nil,
			func(c Config) bool { return c.Scoring.Thresholds.Ban == 1 },
		},
		{
			"aliases resolve", "pacing: {group_ceiling_per_min: &n 5, max_kick_batch: *n}\n", nil,
			func(c Config) bool { return c.Pacing.GroupCeilingPerMin == 5 && c.Pacing.MaxKickBatch == 5 },
		},
		{
			"weights land", "scoring:\n  weights: {new_member: 0.8, link_new_domain: 2}\n", nil,
			func(c Config) bool { return len(c.Scoring.Weights) == 2 && c.Scoring.Weights["link_new_domain"] == 2 },
		},
		{
			"api key from the named variable", "ai: {provider: anthropic, model: m, api_key_env: MY_KEY}\n",
			map[string]string{"MY_KEY": " k1 ", "AI_API_KEY": "other"},
			func(c Config) bool { return c.AI.Enabled() && c.AI.APIKey == "k1" },
		},
		{
			"env timeout wins", "ai: {timeout: 30s}\n",
			map[string]string{"AI_TIMEOUT": "2s"},
			func(c Config) bool { return c.AI.Timeout == 2*time.Second },
		},
		{
			"local model needs no key", "",
			map[string]string{
				"AI_PROVIDER": "openai_compatible", "AI_MODEL": "llama3", "AI_BASE_URL": "http://ollama:11434/v1",
			},
			func(c Config) bool { return c.AI.Enabled() && c.AI.APIKey == "" },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadWith(t, tt.file, tt.env)
			if err != nil {
				t.Fatalf("Load() error = %v, want nil", err)
			}
			if !tt.check(cfg) {
				t.Fatalf("Load() = %+v, did not satisfy the case", cfg)
			}
		})
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		env     map[string]string
		wantErr string // the key or variable the error must name
	}{
		{"typo", "pacing:\n  ceiling_per_mn: 5\n", nil, "pacing.ceiling_per_mn: unknown key"},
		{"fraction for a count", "pacing: {burst: 10.5}\n", nil, "pacing.burst: want a whole number"},
		{"quoted number for a count", "pacing: {burst: \"10\"}\n", nil, "pacing.burst"},
		{"duration without unit", "ai: {timeout: 5}\n", nil, "ai.timeout: want a duration"},
		{"list for a section", "pacing: [1, 2]\n", nil, "pacing: want a section"},
		{"list at the top", "- pacing\n", nil, "file: want a mapping"},
		{"set twice", "presets: {default: strict}\npresets: {default: relaxed}\n", nil, "presets: set twice"},
		{"two documents", "pacing: {}\n---\nai: {}\n", nil, "single YAML document"},
		{"bad yaml", "pacing: {burst: 1\n", nil, "invalid YAML"},
		{"merge key", "base: &b {burst: 1}\npacing:\n  <<: *b\n", nil, "base: unknown key"},
		{"nan weight", "scoring: {weights: {a: .nan}}\n", nil, "scoring.weights.a: must be a finite"},
		{"null weight", "scoring: {weights: {a: }}\n", nil, "scoring.weights.a: want a number"},
		{"negative jitter", "pacing: {spacing_jitter: -1s}\n", nil, "pacing.spacing_jitter"},
		{"zero slowdown", "pacing: {slowdown_factor: 0}\n", nil, "pacing.slowdown_factor"},
		{"floor above ceiling", "pacing: {min_rate_per_min: 30}\n", nil, "pacing.min_rate_per_min"},
		{"thresholds out of order", "scoring: {thresholds: {review: 0.9}}\n", nil, "scoring.thresholds:"},
		{"custom preset", "presets: {default: custom}\n", nil, "presets.default: custom"},
		{"bad key variable name", "ai: {api_key_env: \"\"}\n", nil, "ai.api_key_env"},
		{"base url without scheme", "ai: {base_url: ollama:11434}\n", nil, "ai.base_url"},
		{"env provider unknown", "", map[string]string{"AI_PROVIDER": "openai"}, "AI_PROVIDER (overrides ai.provider)"},
		{"env timeout bad", "", map[string]string{"AI_TIMEOUT": "soon"}, "AI_TIMEOUT"},
		{"env timeout negative", "", map[string]string{"AI_TIMEOUT": "-5s"}, "AI_TIMEOUT (overrides ai.timeout)"},
		{"google without key", "", map[string]string{"AI_PROVIDER": "google", "AI_MODEL": "gemini"}, "AI_API_KEY: required"},
		{"compatible without url", "", map[string]string{"AI_PROVIDER": "openai_compatible", "AI_MODEL": "m"}, "ai.base_url: required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadWith(t, tt.file, tt.env)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load() = %+v, %v; want an error containing %q (misconfiguration must stop start-up and name the key)", cfg, err, tt.wantErr)
			}
		})
	}
}

func TestLoadNeverEchoesSecrets(t *testing.T) {
	// Built at run time so no key-shaped literal sits in the source (secret scanners flag them).
	secret := "sk-" + "ant-" + "secret-" + strings.Repeat("0", 16)
	files := []string{
		"ai:\n  api_key: " + secret + "\n",
		"ai:\n  api_key_env: " + secret + "\n",
		"ai:\n  timeout: " + secret + "\n",
		"ai:\n  provider: " + secret + "\n",
		"ai:\n  base_url: " + secret + "\n",
		"pacing:\n  burst: " + secret + "\n",
	}
	for _, file := range files {
		_, err := loadWith(t, file, map[string]string{"AI_API_KEY": secret})
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("Load(%q) error = %v; want an error that does not contain the secret", file, err)
		}
	}
	_, err := loadWith(t, "", map[string]string{"AI_API_KEY": secret, "AI_PROVIDER": "anthropic"})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("Load() error = %v; want an error (no model) that does not contain the API key", err)
	}
}

func TestExampleFileLoadsDefaults(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	cfg, err := Load(func(k string) string { return map[string]string{"CONFIG_FILE": path}[k] })
	if err != nil {
		t.Fatalf("Load(config.example.yaml) error = %v", err)
	}
	if want := Defaults(); !reflect.DeepEqual(cfg, want) {
		t.Fatalf("Load(config.example.yaml) = %+v, want Defaults() %+v (the example documents the defaults)", cfg, want)
	}
}

func FuzzDecodeFile(f *testing.F) {
	example, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{
		string(example), "", "# only\n", "\xEF\xBB\xBFpacing:\r\n  burst: 5\r\n",
		"a: &a [*a]\n", "scoring: {weights: {x: 1e309}}\n", "pacing: {burst: 99999999999999999999}\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		first, second := Defaults(), Defaults()
		err1 := decodeFile(&first, data)
		err2 := decodeFile(&second, data)
		if (err1 == nil) != (err2 == nil) || !reflect.DeepEqual(first, second) {
			t.Fatalf("decodeFile is not deterministic: %v / %v", err1, err2)
		}
		if err1 == nil {
			_ = first.validate(keyNames{}) // must not panic on any decoded value
		}
	})
}
