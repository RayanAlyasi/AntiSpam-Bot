package config

import "time"

// Stub declarations for F-002 (see .agent/contracts/F-002.md); the builder implements them,
// and also adds the Pacing, Presets, Scoring, AI and Warnings fields to Config.

// Pacing holds the outbound write pacing defaults (DESIGN.md §10.3, §10.4).
type Pacing struct {
	CeilingPerMin      int
	Burst              int
	SpacingBase        time.Duration
	SpacingJitter      time.Duration
	GroupCeilingPerMin int
	SlowdownFactor     float64
	MinRatePerMin      int
	RecoveryStepPerMin int
	RecoveryInterval   time.Duration
	MaxKickBatch       int
	DailyAlertWrites   int
}

// Presets holds the preset defaults (DESIGN.md §12.2).
type Presets struct {
	Default string
}

// Scoring holds the scoring defaults (DESIGN.md §7.3).
type Scoring struct {
	Thresholds Thresholds
	Weights    map[string]float64
}

// Thresholds are the default score lines (DESIGN.md §7.3, §12.1).
type Thresholds struct {
	Review float64
	Delete float64
	Ban    float64
}

// AI holds the AI classifier settings (DESIGN.md §7.4).
type AI struct {
	Provider         string
	BaseURL          string
	Model            string
	APIKeyEnv        string
	APIKey           string
	Timeout          time.Duration
	StructuredOutput string
	SpamConfidence   float64
}

// Enabled reports whether the AI tier is configured.
func (AI) Enabled() bool { panic("not implemented") }

// Defaults returns the configuration used when no file or variable sets a value.
func Defaults() Config { panic("not implemented") }
