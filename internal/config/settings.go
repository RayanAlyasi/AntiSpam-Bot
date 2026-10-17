package config

import "time"

// The yaml tags are the operator-facing config.yaml keys (DESIGN.md §19.1, §23.7): renaming one
// is a breaking change.

// Pacing holds the outbound write pacing defaults (DESIGN.md §10.3, §10.4).
type Pacing struct {
	CeilingPerMin      int           `yaml:"ceiling_per_min"`       // sustained writes/min; the rate never rises above it on its own
	Burst              int           `yaml:"burst"`                 // writes allowed at the start of a wave
	SpacingBase        time.Duration `yaml:"spacing_base"`          // pause after each write
	SpacingJitter      time.Duration `yaml:"spacing_jitter"`        // random extra pause, 0 to this
	GroupCeilingPerMin int           `yaml:"group_ceiling_per_min"` // per-group bucket
	SlowdownFactor     float64       `yaml:"slowdown_factor"`       // rate multiplier on a warning sign
	MinRatePerMin      int           `yaml:"min_rate_per_min"`      // floor after slowing down
	RecoveryStepPerMin int           `yaml:"recovery_step_per_min"` // rate regained per calm interval
	RecoveryInterval   time.Duration `yaml:"recovery_interval"`     // calm time per recovery step
	MaxKickBatch       int           `yaml:"max_kick_batch"`        // participants per removal call
	DailyAlertWrites   int           `yaml:"daily_alert_writes"`    // operator alert level, not a limit
}

// Presets holds the preset defaults (DESIGN.md §12.2).
type Presets struct {
	Default string `yaml:"default"` // relaxed, standard, strict or ask_admins
}

// Scoring holds the scoring defaults (DESIGN.md §7.3).
type Scoring struct {
	Thresholds Thresholds         `yaml:"thresholds"`
	Weights    map[string]float64 `yaml:"weights"` // signal name → log-odds weight
}

// Thresholds are the default score lines (DESIGN.md §7.3, §12.1).
type Thresholds struct {
	Review float64 `yaml:"review"`
	Delete float64 `yaml:"delete"`
	Ban    float64 `yaml:"ban"`
}

// AI holds the AI classifier settings (DESIGN.md §7.4).
type AI struct {
	Provider         string        `yaml:"provider"` // "" (off), anthropic, openai_compatible or google
	BaseURL          string        `yaml:"base_url"`
	Model            string        `yaml:"model"`
	APIKeyEnv        string        `yaml:"api_key_env"` // the env var holding the API key
	APIKey           string        `yaml:"-"`           // read from APIKeyEnv only, never from the file
	Timeout          time.Duration `yaml:"timeout"`
	StructuredOutput string        `yaml:"structured_output"` // auto, native or json
	SpamConfidence   float64       `yaml:"spam_confidence"`   // AI confidence needed to reach the delete line
}

// Enabled reports whether the AI tier is configured. Without a provider the uncertain band
// goes to admins (DESIGN.md §7.4).
func (a AI) Enabled() bool { return a.Provider != "" }

// DefaultHTTPAddr is where the HTTP server listens when HTTP_ADDR is unset.
const DefaultHTTPAddr = ":8080"

// Defaults returns the configuration used when no file or variable sets a value.
// Each call returns fresh maps, so callers may modify the result.
func Defaults() Config {
	return Config{
		HTTPAddr: DefaultHTTPAddr,
		LogLevel: defaultLogLevel,
		Pacing: Pacing{
			CeilingPerMin:      defaultCeilingPerMin,
			Burst:              defaultBurst,
			SpacingBase:        time.Second,
			SpacingJitter:      1500 * time.Millisecond,
			GroupCeilingPerMin: 10,
			SlowdownFactor:     0.25,
			MinRatePerMin:      2,
			RecoveryStepPerMin: 1,
			RecoveryInterval:   15 * time.Minute,
			MaxKickBatch:       8,
			DailyAlertWrites:   7500,
		},
		Presets: Presets{Default: "standard"},
		Scoring: Scoring{
			Thresholds: Thresholds{Review: 0.60, Delete: 0.85, Ban: 0.95},
			Weights:    map[string]float64{},
		},
		AI: AI{
			APIKeyEnv:        "AI_API_KEY",
			Timeout:          5 * time.Second,
			StructuredOutput: "auto",
			SpamConfidence:   0.90,
		},
	}
}

// Raising these above their defaults raises the ban risk, so Load warns (DESIGN.md §10.3).
const (
	defaultCeilingPerMin = 20
	defaultBurst         = 10
)
