// Acceptance tests for F-002 (.agent/contracts/F-002.md): an optional config.yaml with safe
// defaults, validated at start, with the documented environment variables overriding it.
package config_test

import (
	"bufio"
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/config"
)

// envFunc turns a map into the getenv function Load takes.
func envFunc(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

// loadYAML writes content to a temporary config.yaml, points CONFIG_FILE at it and loads.
func loadYAML(t *testing.T, content string, env map[string]string) (config.Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	e := maps.Clone(env)
	if e == nil {
		e = map[string]string{}
	}
	e["CONFIG_FILE"] = path
	return config.Load(envFunc(e))
}

func mustLoadYAML(t *testing.T, content string, env map[string]string) config.Config {
	t.Helper()
	cfg, err := loadYAML(t, content, env)
	if err != nil {
		t.Fatalf("Load() error = %v, want none for config.yaml:\n%s", err, content)
	}
	return cfg
}

// loadNoFile loads from an empty working directory with CONFIG_FILE unset, so no file exists.
func loadNoFile(t *testing.T, env map[string]string) (config.Config, error) {
	t.Helper()
	t.Chdir(t.TempDir())
	return config.Load(envFunc(env))
}

// settingsDiff lists the F-002 settings that differ between got and want ("" if none).
func settingsDiff(got, want config.Config) string {
	var d []string
	if got.Pacing != want.Pacing {
		d = append(d, fmt.Sprintf("Pacing = %+v, want %+v", got.Pacing, want.Pacing))
	}
	if got.Presets != want.Presets {
		d = append(d, fmt.Sprintf("Presets = %+v, want %+v", got.Presets, want.Presets))
	}
	if got.Scoring.Thresholds != want.Scoring.Thresholds {
		d = append(d, fmt.Sprintf("Scoring.Thresholds = %+v, want %+v", got.Scoring.Thresholds, want.Scoring.Thresholds))
	}
	if !maps.Equal(got.Scoring.Weights, want.Scoring.Weights) {
		d = append(d, fmt.Sprintf("Scoring.Weights = %v, want %v", got.Scoring.Weights, want.Scoring.Weights))
	}
	if got.AI != want.AI {
		d = append(d, fmt.Sprintf("AI = %+v, want %+v", got.AI, want.AI))
	}
	return strings.Join(d, "; ")
}

func TestDefaultsMatchDesign(t *testing.T) {
	d := config.Defaults()
	tests := []struct {
		key       string
		got, want any
		ref       string
	}{
		{"pacing.ceiling_per_min", d.Pacing.CeilingPerMin, 20, "§10.3 ceiling 20 writes/min"},
		{"pacing.burst", d.Pacing.Burst, 10, "§10.3 burst allowance of 10"},
		{"pacing.spacing_base", d.Pacing.SpacingBase, time.Second, "§10.3 base ~1 s"},
		{"pacing.spacing_jitter", d.Pacing.SpacingJitter, 1500 * time.Millisecond, "§10.3 jitter 0-1.5 s"},
		{"pacing.group_ceiling_per_min", d.Pacing.GroupCeilingPerMin, 10, "§10.3 per-group bucket 10/min"},
		{"pacing.slowdown_factor", d.Pacing.SlowdownFactor, 0.25, "§10.3 drop to a quarter"},
		{"pacing.min_rate_per_min", d.Pacing.MinRatePerMin, 2, "§10.3 minimum 2 writes/min"},
		{"pacing.recovery_step_per_min", d.Pacing.RecoveryStepPerMin, 1, "§10.3 +1 write/min"},
		{"pacing.recovery_interval", d.Pacing.RecoveryInterval, 15 * time.Minute, "§10.3 every 15 calm minutes"},
		{"pacing.max_kick_batch", d.Pacing.MaxKickBatch, 8, "§10.4 batch cap 8"},
		{"pacing.daily_alert_writes", d.Pacing.DailyAlertWrites, 7500, "§10.3 daily alert at 7,500"},
		{"presets.default", d.Presets.Default, "standard", "§12.2 Standard is the default preset"},
		{"scoring.thresholds.review", d.Scoring.Thresholds.Review, 0.60, "§7.3/§12.1 T_review 0.60"},
		{"scoring.thresholds.delete", d.Scoring.Thresholds.Delete, 0.85, "§12.1 delete 0.85"},
		{"scoring.thresholds.ban", d.Scoring.Thresholds.Ban, 0.95, "§12.1 ban 0.95"},
		{"scoring.weights (count)", len(d.Scoring.Weights), 0, "no weight overrides by default"},
		{"ai.provider", d.AI.Provider, "", "§19.1 the AI tier stays off unless configured"},
		{"ai.base_url", d.AI.BaseURL, "", "provider default endpoint"},
		{"ai.model", d.AI.Model, "", "no model until configured"},
		{"ai.api_key_env", d.AI.APIKeyEnv, "AI_API_KEY", "§7.4 example api_key_env: AI_API_KEY"},
		{"ai.api_key (value)", d.AI.APIKey, "", "no key without the environment"},
		{"ai.timeout", d.AI.Timeout, 5 * time.Second, "§7.4 default 5 s"},
		{"ai.structured_output", d.AI.StructuredOutput, "auto", "§7.4 structured_output: auto"},
		{"ai.spam_confidence", d.AI.SpamConfidence, 0.90, "§7.3 ai.spam_confidence 0.9"},
		{"log level", d.LogLevel, zerolog.InfoLevel, "F-000 default info"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("Defaults() %s = %v, want %v (DESIGN %s)", tt.key, tt.got, tt.want, tt.ref)
		}
	}
	if d.AI.Enabled() {
		t.Errorf("Defaults().AI.Enabled() = true, want false (no AI configured means the AI tier is off, DESIGN §19.1)")
	}
	if len(d.Warnings) != 0 {
		t.Errorf("Defaults().Warnings = %q, want none (defaults are the safe settings)", d.Warnings)
	}
}

func TestLoadWithoutFileUsesDefaults(t *testing.T) {
	cfg, err := loadNoFile(t, map[string]string{})
	if err != nil {
		t.Fatalf("Load() with no config.yaml error = %v, want none (the bot starts with no config file at all, DESIGN §19.1)", err)
	}
	if diff := settingsDiff(cfg, config.Defaults()); diff != "" {
		t.Errorf("Load() with no file: %s (missing file means defaults, DESIGN §19.1)", diff)
	}
	if cfg.LogLevel != zerolog.InfoLevel {
		t.Errorf("LogLevel = %v, want info (F-000 default)", cfg.LogLevel)
	}
	if cfg.AI.Enabled() {
		t.Errorf("AI.Enabled() = true, want false (no AI settings means the AI tier is off, DESIGN §19.1)")
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("Warnings = %q, want none for defaults", cfg.Warnings)
	}
}

func TestLoadReadsDefaultPathInWorkingDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("pacing:\n  burst: 4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	cfg, err := config.Load(envFunc(map[string]string{}))
	if err != nil {
		t.Fatalf("Load() error = %v, want none", err)
	}
	if cfg.Pacing.Burst != 4 {
		t.Errorf("Pacing.Burst = %d, want 4 (with CONFIG_FILE unset, ./config.yaml is read)", cfg.Pacing.Burst)
	}
}

func TestExplicitConfigFileMissingIsError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.yaml")
	cfg, err := config.Load(envFunc(map[string]string{"CONFIG_FILE": missing}))
	if err == nil {
		t.Fatalf("Load() = %+v, want an error (an explicitly named CONFIG_FILE that doesn't exist is a mistake, not a default)", cfg)
	}
	if !strings.Contains(err.Error(), "nope.yaml") {
		t.Errorf("error %q doesn't name the path, want it to mention nope.yaml so the operator can fix it", err)
	}
	if _, err := config.Load(envFunc(map[string]string{"CONFIG_FILE": t.TempDir()})); err == nil {
		t.Errorf("Load() with CONFIG_FILE pointing at a directory: got no error, want one (it can't be read as a file)")
	}
}

func TestEveryKeyIsHonoured(t *testing.T) {
	tests := []struct {
		key  string
		yaml string
		env  map[string]string
		got  func(config.Config) any
		want any
	}{
		{"pacing.ceiling_per_min", "pacing:\n  ceiling_per_min: 12\n", nil, func(c config.Config) any { return c.Pacing.CeilingPerMin }, 12},
		{"pacing.burst", "pacing:\n  burst: 4\n", nil, func(c config.Config) any { return c.Pacing.Burst }, 4},
		{"pacing.spacing_base", "pacing:\n  spacing_base: 2s\n", nil, func(c config.Config) any { return c.Pacing.SpacingBase }, 2 * time.Second},
		{"pacing.spacing_jitter", "pacing:\n  spacing_jitter: 3s\n", nil, func(c config.Config) any { return c.Pacing.SpacingJitter }, 3 * time.Second},
		{"pacing.group_ceiling_per_min", "pacing:\n  group_ceiling_per_min: 6\n", nil, func(c config.Config) any { return c.Pacing.GroupCeilingPerMin }, 6},
		{"pacing.slowdown_factor", "pacing:\n  slowdown_factor: 0.5\n", nil, func(c config.Config) any { return c.Pacing.SlowdownFactor }, 0.5},
		{"pacing.min_rate_per_min", "pacing:\n  min_rate_per_min: 3\n", nil, func(c config.Config) any { return c.Pacing.MinRatePerMin }, 3},
		{"pacing.recovery_step_per_min", "pacing:\n  recovery_step_per_min: 2\n", nil, func(c config.Config) any { return c.Pacing.RecoveryStepPerMin }, 2},
		{"pacing.recovery_interval", "pacing:\n  recovery_interval: 30m\n", nil, func(c config.Config) any { return c.Pacing.RecoveryInterval }, 30 * time.Minute},
		{"pacing.max_kick_batch", "pacing:\n  max_kick_batch: 5\n", nil, func(c config.Config) any { return c.Pacing.MaxKickBatch }, 5},
		{"pacing.daily_alert_writes", "pacing:\n  daily_alert_writes: 5000\n", nil, func(c config.Config) any { return c.Pacing.DailyAlertWrites }, 5000},
		{"presets.default", "presets:\n  default: strict\n", nil, func(c config.Config) any { return c.Presets.Default }, "strict"},
		{"presets.default ask_admins", "presets:\n  default: ask_admins\n", nil, func(c config.Config) any { return c.Presets.Default }, "ask_admins"},
		{"presets.default relaxed", "presets:\n  default: relaxed\n", nil, func(c config.Config) any { return c.Presets.Default }, "relaxed"},
		{"scoring.thresholds.review", "scoring:\n  thresholds:\n    review: 0.5\n", nil, func(c config.Config) any { return c.Scoring.Thresholds.Review }, 0.5},
		{"scoring.thresholds.delete", "scoring:\n  thresholds:\n    delete: 0.8\n", nil, func(c config.Config) any { return c.Scoring.Thresholds.Delete }, 0.8},
		{"scoring.thresholds.ban", "scoring:\n  thresholds:\n    ban: 0.9\n", nil, func(c config.Config) any { return c.Scoring.Thresholds.Ban }, 0.9},
		{"scoring.weights", "scoring:\n  weights:\n    link_new_domain: 2.1\n    just_joined: -0.5\n", nil,
			func(c config.Config) any { return fmt.Sprint(c.Scoring.Weights) }, fmt.Sprint(map[string]float64{"link_new_domain": 2.1, "just_joined": -0.5})},
		{"ai.provider", "ai:\n  provider: google\n  model: m\n", map[string]string{"AI_API_KEY": "k"}, func(c config.Config) any { return c.AI.Provider }, "google"},
		{"ai.base_url", "ai:\n  base_url: https://llm.example.org/v1\n", nil, func(c config.Config) any { return c.AI.BaseURL }, "https://llm.example.org/v1"},
		{"ai.model", "ai:\n  model: some-model\n", nil, func(c config.Config) any { return c.AI.Model }, "some-model"},
		{"ai.api_key_env", "ai:\n  api_key_env: OTHER_KEY\n", nil, func(c config.Config) any { return c.AI.APIKeyEnv }, "OTHER_KEY"},
		{"ai.timeout", "ai:\n  timeout: 20s\n", nil, func(c config.Config) any { return c.AI.Timeout }, 20 * time.Second},
		{"ai.structured_output", "ai:\n  structured_output: json\n", nil, func(c config.Config) any { return c.AI.StructuredOutput }, "json"},
		{"ai.structured_output native", "ai:\n  structured_output: native\n", nil, func(c config.Config) any { return c.AI.StructuredOutput }, "native"},
		{"ai.spam_confidence", "ai:\n  spam_confidence: 0.95\n", nil, func(c config.Config) any { return c.AI.SpamConfidence }, 0.95},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			cfg := mustLoadYAML(t, tt.yaml, tt.env)
			if got := tt.got(cfg); got != tt.want {
				t.Errorf("%s = %v, want %v (the key in config.yaml must set its field; key names are operator API, DESIGN §19.1, §23.7)", tt.key, got, tt.want)
			}
		})
	}
}

func TestPartialFileKeepsOtherDefaults(t *testing.T) {
	d := config.Defaults()
	burst := d
	burst.Pacing.Burst = 5
	review := d
	review.Scoring.Thresholds.Review = 0.5
	model := d
	model.AI.Model = "x"
	tests := []struct {
		name string
		yaml string
		want config.Config
	}{
		{"one pacing key", "pacing:\n  burst: 5\n", burst},
		{"one nested threshold", "scoring:\n  thresholds:\n    review: 0.5\n", review},
		{"one ai key", "ai:\n  model: x\n", model},
		{"empty sections", "pacing:\npresets:\nscoring:\n  thresholds:\nai:\n", d},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := mustLoadYAML(t, tt.yaml, nil)
			if diff := settingsDiff(cfg, tt.want); diff != "" {
				t.Errorf("%s (keys a file leaves out keep their defaults; a partial file must never zero them, DESIGN §19.1)", diff)
			}
		})
	}
}

func TestEmptyCommentBOMAndCRLFFiles(t *testing.T) {
	d := config.Defaults()
	burst := d
	burst.Pacing.Burst = 5
	tests := []struct {
		name string
		yaml string
		want config.Config
	}{
		{"empty file", "", d},
		{"comments only", "# pacing:\n#   burst: 30\n\n", d},
		{"UTF-8 BOM", "\ufeffpacing:\n  burst: 5\n", burst},
		{"CRLF line endings", "pacing:\r\n  burst: 5\r\n", burst},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := mustLoadYAML(t, tt.yaml, nil)
			if diff := settingsDiff(cfg, tt.want); diff != "" {
				t.Errorf("%s (files saved by common editors must load as written)", diff)
			}
		})
	}
}

func TestDesignAIExampleLoads(t *testing.T) {
	// The example configuration from DESIGN.md §7.4, verbatim.
	const example = `ai:
  provider: openai_compatible        # anthropic | openai_compatible | google
  base_url: http://ollama:11434/v1   # a local server; or a hosted endpoint
  model: <model-name>
  api_key_env: AI_API_KEY            # empty for most local servers
  timeout: 20s
  structured_output: auto            # native schema if supported, else JSON mode + validation
`
	cfg := mustLoadYAML(t, example, nil)
	want := config.Defaults().AI
	want.Provider, want.BaseURL, want.Model, want.Timeout = "openai_compatible", "http://ollama:11434/v1", "<model-name>", 20*time.Second
	if cfg.AI != want {
		t.Errorf("AI = %+v, want %+v (the documented example must load as written, DESIGN §7.4)", cfg.AI, want)
	}
	if !cfg.AI.Enabled() {
		t.Errorf("AI.Enabled() = false, want true (provider, model and base URL are set; local servers need no key, DESIGN §7.4)")
	}
}

func TestEnvOverridesFile(t *testing.T) {
	const file = "ai:\n  provider: anthropic\n  model: file-model\n  timeout: 20s\n  base_url: https://file.example/v1\n"
	t.Run("AI variables beat the file", func(t *testing.T) {
		cfg := mustLoadYAML(t, file, map[string]string{
			"AI_API_KEY":  "k",
			"AI_PROVIDER": "google",
			"AI_MODEL":    "env-model",
			"AI_TIMEOUT":  "45s",
			"AI_BASE_URL": "https://env.example/v1",
		})
		got := [4]any{cfg.AI.Provider, cfg.AI.Model, cfg.AI.Timeout, cfg.AI.BaseURL}
		want := [4]any{"google", "env-model", 45 * time.Second, "https://env.example/v1"}
		if got != want {
			t.Errorf("provider, model, timeout, base URL = %v, want %v (environment variables override config.yaml, DESIGN §19.1)", got, want)
		}
	})
	t.Run("blank variables don't override", func(t *testing.T) {
		cfg := mustLoadYAML(t, file, map[string]string{"AI_API_KEY": "k", "AI_MODEL": "   ", "AI_PROVIDER": "", "AI_TIMEOUT": " "})
		if cfg.AI.Model != "file-model" || cfg.AI.Provider != "anthropic" || cfg.AI.Timeout != 20*time.Second {
			t.Errorf("AI = %+v, want the file's provider, model and timeout (an empty or blank variable is unset, not an override)", cfg.AI)
		}
	})
	t.Run("environment completes a partial file", func(t *testing.T) {
		cfg := mustLoadYAML(t, "ai:\n  provider: openai_compatible\n  base_url: http://ollama:11434/v1\n", map[string]string{"AI_MODEL": "m"})
		if !cfg.AI.Enabled() || cfg.AI.Model != "m" {
			t.Errorf("AI = %+v, Enabled() = %v; want model m and enabled (overrides apply before validation)", cfg.AI, cfg.AI.Enabled())
		}
	})
	t.Run("F-000 variables still work with a file", func(t *testing.T) {
		cfg := mustLoadYAML(t, "pacing:\n  burst: 5\n", map[string]string{
			"DATABASE_URL":    "postgres://bot@db/antispam",
			"PUBLIC_URL":      "https://bot.example.org",
			"OPERATOR_PHONES": "+966500000001,+966500000002",
			"LOG_LEVEL":       "debug",
		})
		if cfg.DatabaseURL != "postgres://bot@db/antispam" || cfg.PublicURL != "https://bot.example.org" ||
			!slices.Equal(cfg.OperatorPhones, []string{"+966500000001", "+966500000002"}) || cfg.LogLevel != zerolog.DebugLevel {
			t.Errorf("Config = %+v, want the F-000 environment settings applied alongside the file", cfg)
		}
		if cfg.Pacing.Burst != 5 {
			t.Errorf("Pacing.Burst = %d, want 5 from the file", cfg.Pacing.Burst)
		}
	})
}

func TestAPIKeyComesFromNamedEnvVar(t *testing.T) {
	const file = "ai:\n  provider: anthropic\n  model: m\n"
	cfg := mustLoadYAML(t, file, map[string]string{"AI_API_KEY": "secret-default"})
	if cfg.AI.APIKey != "secret-default" || cfg.AI.APIKeyEnv != "AI_API_KEY" {
		t.Errorf("APIKey, APIKeyEnv = %q, %q; want %q, %q (the key is read from AI_API_KEY by default, DESIGN §19.1)",
			cfg.AI.APIKey, cfg.AI.APIKeyEnv, "secret-default", "AI_API_KEY")
	}
	cfg = mustLoadYAML(t, file+"  api_key_env: LOCAL_AI_KEY\n", map[string]string{"LOCAL_AI_KEY": "secret-local", "AI_API_KEY": "secret-default"})
	if cfg.AI.APIKey != "secret-local" {
		t.Errorf("APIKey = %q, want %q (ai.api_key_env names the variable that holds the key, DESIGN §7.4)", cfg.AI.APIKey, "secret-local")
	}
}

func TestAIRequirements(t *testing.T) {
	tests := []struct {
		name        string
		yaml        string
		env         map[string]string
		wantErrWith string // "" means no error expected
		wantEnabled bool
	}{
		{"model alone keeps AI off", "", map[string]string{"AI_MODEL": "m"}, "", false},
		{"anthropic with model and key", "ai:\n  provider: anthropic\n  model: m\n", map[string]string{"AI_API_KEY": "k"}, "", true},
		{"google with model and key", "ai:\n  provider: google\n  model: m\n", map[string]string{"AI_API_KEY": "k"}, "", true},
		{"openai_compatible needs no key", "ai:\n  provider: openai_compatible\n  model: m\n  base_url: http://ollama:11434/v1\n", nil, "", true},
		{"anthropic without key", "ai:\n  provider: anthropic\n  model: m\n", nil, "AI_API_KEY", false},
		{"google without key", "ai:\n  provider: google\n  model: m\n  api_key_env: GEMINI_KEY\n", nil, "GEMINI_KEY", false},
		{"provider without model", "ai:\n  provider: anthropic\n", map[string]string{"AI_API_KEY": "k"}, "ai.model", false},
		{"openai_compatible without base_url", "ai:\n  provider: openai_compatible\n  model: m\n", nil, "ai.base_url", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadYAML(t, tt.yaml, tt.env)
			if tt.wantErrWith != "" {
				if err == nil {
					t.Fatalf("Load() AI = %+v, want an error mentioning %s (a half-configured provider must fail at start, not run blind)", cfg.AI, tt.wantErrWith)
				}
				if !strings.Contains(err.Error(), tt.wantErrWith) {
					t.Errorf("error %q, want it to mention %s so the operator knows what to set", err, tt.wantErrWith)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v, want none", err)
			}
			if cfg.AI.Enabled() != tt.wantEnabled {
				t.Errorf("AI.Enabled() = %v, want %v (AI is on only with a provider set, DESIGN §19.1)", cfg.AI.Enabled(), tt.wantEnabled)
			}
		})
	}
	t.Run("environment only, no file", func(t *testing.T) {
		cfg, err := loadNoFile(t, map[string]string{"AI_PROVIDER": "anthropic", "AI_MODEL": "m", "AI_API_KEY": "k"})
		if err != nil {
			t.Fatalf("Load() error = %v, want none (AI can be configured by environment alone, DESIGN §19.1)", err)
		}
		if !cfg.AI.Enabled() || cfg.AI.Timeout != 5*time.Second {
			t.Errorf("AI = %+v, Enabled() = %v; want enabled with the 5s default timeout (DESIGN §7.4)", cfg.AI, cfg.AI.Enabled())
		}
	})
}

func TestInvalidConfigIsRejected(t *testing.T) {
	aiOn := "ai:\n  provider: openai_compatible\n  model: m\n  base_url: http://ollama:11434/v1\n"
	tests := []struct {
		name    string
		yaml    string
		env     map[string]string
		wantAny []string // the error must mention one of these; nil means any error
	}{
		{"zero ceiling", "pacing:\n  ceiling_per_min: 0\n", nil, []string{"pacing.ceiling_per_min"}},
		{"negative ceiling", "pacing:\n  ceiling_per_min: -5\n", nil, []string{"pacing.ceiling_per_min"}},
		{"zero burst", "pacing:\n  burst: 0\n", nil, []string{"pacing.burst"}},
		{"zero group ceiling", "pacing:\n  group_ceiling_per_min: 0\n", nil, []string{"pacing.group_ceiling_per_min"}},
		{"zero kick batch", "pacing:\n  max_kick_batch: 0\n", nil, []string{"pacing.max_kick_batch"}},
		{"zero minimum rate", "pacing:\n  min_rate_per_min: 0\n", nil, []string{"pacing.min_rate_per_min"}},
		{"minimum rate above ceiling", "pacing:\n  min_rate_per_min: 30\n", nil, []string{"pacing.min_rate_per_min"}},
		{"slowdown factor 1", "pacing:\n  slowdown_factor: 1\n", nil, []string{"pacing.slowdown_factor"}},
		{"slowdown factor 0", "pacing:\n  slowdown_factor: 0\n", nil, []string{"pacing.slowdown_factor"}},
		{"zero recovery step", "pacing:\n  recovery_step_per_min: 0\n", nil, []string{"pacing.recovery_step_per_min"}},
		{"zero recovery interval", "pacing:\n  recovery_interval: 0s\n", nil, []string{"pacing.recovery_interval"}},
		{"zero spacing base", "pacing:\n  spacing_base: 0s\n", nil, []string{"pacing.spacing_base"}},
		{"negative jitter", "pacing:\n  spacing_jitter: -1s\n", nil, []string{"pacing.spacing_jitter"}},
		{"zero daily alert", "pacing:\n  daily_alert_writes: 0\n", nil, []string{"pacing.daily_alert_writes"}},
		{"unknown preset", "presets:\n  default: lenient\n", nil, []string{"presets.default"}},
		{"custom is per-group only", "presets:\n  default: custom\n", nil, []string{"presets.default"}},
		{"review above delete", "scoring:\n  thresholds:\n    review: 0.9\n", nil, []string{"scoring.thresholds"}},
		{"delete above ban", "scoring:\n  thresholds:\n    delete: 0.97\n", nil, []string{"scoring.thresholds"}},
		{"ban above 1", "scoring:\n  thresholds:\n    ban: 1.5\n", nil, []string{"scoring.thresholds"}},
		{"review zero", "scoring:\n  thresholds:\n    review: 0\n", nil, []string{"scoring.thresholds"}},
		{"NaN weight", "scoring:\n  weights:\n    link: .nan\n", nil, []string{"scoring.weights"}},
		{"infinite weight", "scoring:\n  weights:\n    link: .inf\n", nil, []string{"scoring.weights"}},
		{"unknown provider", "ai:\n  provider: openai\n  model: m\n", nil, []string{"ai.provider"}},
		{"unknown structured output", "ai:\n  structured_output: yes\n", nil, []string{"ai.structured_output"}},
		{"spam confidence above 1", "ai:\n  spam_confidence: 1.2\n", nil, []string{"ai.spam_confidence"}},
		{"spam confidence zero", "ai:\n  spam_confidence: 0\n", nil, []string{"ai.spam_confidence"}},
		{"zero timeout", aiOn + "  timeout: 0s\n", nil, []string{"ai.timeout"}},
		{"ftp base URL", "ai:\n  base_url: ftp://llm.example.org\n", nil, []string{"ai.base_url"}},
		{"base URL without scheme", "ai:\n  base_url: llm.example.org/v1\n", nil, []string{"ai.base_url"}},
		{"typo in a key", "pacing:\n  ceilling_per_min: 30\n", nil, []string{"ceilling_per_min"}},
		{"typo in a section", "pacng:\n  burst: 5\n", nil, []string{"pacng"}},
		{"wrong type", "pacing:\n  ceiling_per_min: fast\n", nil, nil},
		{"duration without unit", "ai:\n  timeout: soon\n", nil, nil},
		{"malformed YAML", "pacing: [unclosed\n", nil, nil},
		{"unknown AI_PROVIDER", "", map[string]string{"AI_PROVIDER": "openai", "AI_MODEL": "m"}, []string{"AI_PROVIDER"}},
		{"unparsable AI_TIMEOUT", aiOn, map[string]string{"AI_TIMEOUT": "soon"}, []string{"AI_TIMEOUT"}},
		{"zero AI_TIMEOUT", aiOn, map[string]string{"AI_TIMEOUT": "0s"}, []string{"AI_TIMEOUT", "ai.timeout"}},
		{"bad AI_BASE_URL", aiOn, map[string]string{"AI_BASE_URL": "not a url"}, []string{"AI_BASE_URL", "ai.base_url"}},
		{"bad LOG_LEVEL with a file", "pacing:\n  burst: 5\n", map[string]string{"LOG_LEVEL": "verbose"}, []string{"LOG_LEVEL"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadYAML(t, tt.yaml, tt.env)
			if err == nil {
				t.Fatalf("Load() = %+v, want an error (an invalid setting must stop the bot at start, not be silently used)", cfg)
			}
			if tt.wantAny != nil && !slices.ContainsFunc(tt.wantAny, func(s string) bool { return strings.Contains(err.Error(), s) }) {
				t.Errorf("error %q, want it to mention %s so the operator knows which setting is wrong", err, strings.Join(tt.wantAny, " or "))
			}
		})
	}
}

func TestSecretsNeverInFileOrErrors(t *testing.T) {
	const secret = "sk-live-SECRET123"
	_, err := loadYAML(t, "ai:\n  provider: anthropic\n  model: m\n  api_key: "+secret+"\n", nil)
	if err == nil {
		t.Fatalf("Load() accepted ai.api_key, want an error (keys come from the environment via ai.api_key_env, never from the file, DESIGN §7.4, §17)")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error %q contains the API key, want it redacted (errors are logged)", err)
	}

	_, err = loadYAML(t, "ai:\n  provider: anthropic\n  model: m\n  spam_confidence: 2\n", map[string]string{"AI_API_KEY": secret})
	if err == nil {
		t.Fatalf("Load() accepted spam_confidence 2, want an error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("validation error %q contains the API key, want it never echoed (errors are logged)", err)
	}

	_, err = loadYAML(t, "ai:\n  provider: anthropic\n  model: m\n  api_key_env: MISSING_KEY\n", map[string]string{"AI_API_KEY": secret})
	if err == nil {
		t.Fatalf("Load() with api_key_env naming an unset variable: no error, want one naming MISSING_KEY")
	}
	if strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "MISSING_KEY") {
		t.Errorf("error %q, want it to name MISSING_KEY and not contain any key value", err)
	}

	cfg := mustLoadYAML(t, "pacing:\n  ceiling_per_min: 40\nai:\n  provider: anthropic\n  model: m\n", map[string]string{"AI_API_KEY": secret})
	for _, w := range cfg.Warnings {
		if strings.Contains(w, secret) {
			t.Errorf("warning %q contains the API key, want it never echoed (warnings are logged)", w)
		}
	}
}

func TestRaisingPacingWarns(t *testing.T) {
	tests := []struct {
		yaml, key string
	}{
		{"pacing:\n  ceiling_per_min: 30\n", "pacing.ceiling_per_min"},
		{"pacing:\n  burst: 20\n", "pacing.burst"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			cfg := mustLoadYAML(t, tt.yaml, nil)
			if !slices.ContainsFunc(cfg.Warnings, func(w string) bool { return strings.Contains(w, tt.key) }) {
				t.Errorf("Warnings = %q, want one naming %s (raising pacing above the default is allowed but risky for the number, DESIGN §10.3, §19.1)", cfg.Warnings, tt.key)
			}
		})
	}
}

func TestSafePacingDoesNotWarn(t *testing.T) {
	tests := []struct{ name, yaml string }{
		{"no file content", ""},
		{"defaults written out", "pacing:\n  ceiling_per_min: 20\n  burst: 10\n"},
		{"lower than defaults", "pacing:\n  ceiling_per_min: 10\n  burst: 3\n  group_ceiling_per_min: 5\n  max_kick_batch: 5\n  spacing_base: 2s\n"},
		{"other sections", "presets:\n  default: strict\nscoring:\n  thresholds:\n    delete: 0.8\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := mustLoadYAML(t, tt.yaml, nil)
			if len(cfg.Warnings) != 0 {
				t.Errorf("Warnings = %q, want none (only raising pacing above its default is risky; warning on safe values trains operators to ignore them)", cfg.Warnings)
			}
		})
	}
}

func TestLoadIsIdempotentAndIsolated(t *testing.T) {
	const file = "pacing:\n  burst: 5\nscoring:\n  weights:\n    link_new_domain: 1.5\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	env := envFunc(map[string]string{"CONFIG_FILE": path})
	first, err := config.Load(env)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	second, err := config.Load(env)
	if err != nil {
		t.Fatalf("second Load() error = %v", err)
	}
	if diff := settingsDiff(second, first); diff != "" {
		t.Errorf("second Load(): %s (loading the same input twice must give the same result)", diff)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, []byte(file)) {
		t.Errorf("config.yaml changed after Load, want it left untouched (Load only reads)")
	}

	first.Scoring.Weights["injected"] = 9
	third, err := config.Load(env)
	if err != nil {
		t.Fatalf("third Load() error = %v", err)
	}
	if _, ok := third.Scoring.Weights["injected"]; ok {
		t.Errorf("Scoring.Weights = %v after mutating an earlier result, want each Load to return its own map", third.Scoring.Weights)
	}

	none, err := loadNoFile(t, map[string]string{})
	if err != nil {
		t.Fatalf("Load() without a file error = %v", err)
	}
	if len(none.Scoring.Weights) != 0 || none.Pacing.Burst != 10 {
		t.Errorf("Load() without a file after loading one: weights %v, burst %d; want no weights and burst 10 (one load must not leak into the next)",
			none.Scoring.Weights, none.Pacing.Burst)
	}
	if w := config.Defaults().Scoring.Weights; len(w) != 0 {
		t.Errorf("Defaults().Scoring.Weights = %v after loads, want empty (defaults must not be shared mutable state)", w)
	}
}

// documentedKeys are the operator-facing config keys (DESIGN §19.1, §23.7).
var documentedKeys = []string{
	"pacing.ceiling_per_min", "pacing.burst", "pacing.spacing_base", "pacing.spacing_jitter",
	"pacing.group_ceiling_per_min", "pacing.slowdown_factor", "pacing.min_rate_per_min",
	"pacing.recovery_step_per_min", "pacing.recovery_interval", "pacing.max_kick_batch",
	"pacing.daily_alert_writes", "presets.default", "scoring.thresholds.review",
	"scoring.thresholds.delete", "scoring.thresholds.ban", "scoring.weights", "ai.provider",
	"ai.base_url", "ai.model", "ai.api_key_env", "ai.timeout", "ai.structured_output",
	"ai.spam_confidence",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

func TestExampleFileDocumentsEveryKey(t *testing.T) {
	path := filepath.Join(repoRoot(t), "config.example.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v; want a config.example.yaml at the repository root documenting every key (each setting has a comment and a safe default, DESIGN §19.1)", path, err)
	}
	cfg, err := config.Load(envFunc(map[string]string{"CONFIG_FILE": path}))
	if err != nil {
		t.Fatalf("Load(config.example.yaml) error = %v, want none (the shipped example must be valid)", err)
	}
	if diff := settingsDiff(cfg, config.Defaults()); diff != "" {
		t.Errorf("config.example.yaml: %s (the example must show the real defaults)", diff)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("config.example.yaml produces warnings %q, want none (it shows the safe defaults)", cfg.Warnings)
	}

	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	for _, key := range documentedKeys {
		leaf := key[strings.LastIndex(key, ".")+1:]
		re := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(leaf) + `\s*:`)
		i := slices.IndexFunc(lines, re.MatchString)
		if i < 0 {
			t.Errorf("config.example.yaml has no %s, want every key listed with its default (DESIGN §19.1)", key)
			continue
		}
		commented := strings.Contains(lines[i], "#")
		for j := i - 1; !commented && j >= 0 && strings.TrimSpace(lines[j]) != ""; j-- {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "#") {
				commented = true
			} else {
				break
			}
		}
		if !commented {
			t.Errorf("config.example.yaml: %s (line %d) has no comment on its line or directly above, want each setting explained (DESIGN §19.1)", key, i+1)
		}
	}
}
