package config

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

var (
	providers         = []string{"anthropic", "openai_compatible", "google"}
	structuredOutputs = []string{"auto", "native", "json"}
	presetNames       = []string{"relaxed", "standard", "strict", "ask_admins"}

	// envVarName is what ai.api_key_env must look like. It is upper-case only on purpose: errors
	// name the variable, and a key pasted here by mistake (keys mix cases or contain "-") must
	// fail this check, which never prints the value, rather than be echoed as a variable name.
	envVarName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

// keyNames maps a dotted key to how errors name it; an env override names the variable.
type keyNames map[string]string

func (n keyNames) of(key string) string {
	if name, ok := n[key]; ok {
		return name
	}
	return key
}

// applyAIEnv applies the AI_* overrides (DESIGN.md §19.1) and reads the API key from the
// variable named by ai.api_key_env. Empty or blank values don't override.
func applyAIEnv(ai *AI, getenv func(string) string) (keyNames, error) {
	names := keyNames{}
	for _, o := range []struct {
		env, key string
		field    *string
	}{
		{"AI_PROVIDER", "ai.provider", &ai.Provider},
		{"AI_BASE_URL", "ai.base_url", &ai.BaseURL},
		{"AI_MODEL", "ai.model", &ai.Model},
	} {
		if v := strings.TrimSpace(getenv(o.env)); v != "" {
			*o.field = v
			names[o.key] = o.env + " (overrides " + o.key + ")"
		}
	}
	if v := strings.TrimSpace(getenv("AI_TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, errors.New("AI_TIMEOUT: want a duration such as 5s or 30s")
		}
		ai.Timeout = d
		names["ai.timeout"] = "AI_TIMEOUT (overrides ai.timeout)"
	}
	if envVarName.MatchString(ai.APIKeyEnv) {
		ai.APIKey = strings.TrimSpace(getenv(ai.APIKeyEnv))
	}
	return names, nil
}

// validate reports every invalid setting, each naming its dotted key or env var. Messages
// never quote string values, so a secret typed into the wrong place is not echoed.
func (c *Config) validate(names keyNames) error {
	var errs []error
	bad := func(key, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: "+format, append([]any{names.of(key)}, args...)...))
	}

	p := c.Pacing
	for _, f := range []struct {
		key string
		v   int
	}{
		{"pacing.ceiling_per_min", p.CeilingPerMin},
		{"pacing.burst", p.Burst},
		{"pacing.group_ceiling_per_min", p.GroupCeilingPerMin},
		{"pacing.min_rate_per_min", p.MinRatePerMin},
		{"pacing.recovery_step_per_min", p.RecoveryStepPerMin},
		{"pacing.max_kick_batch", p.MaxKickBatch},
		{"pacing.daily_alert_writes", p.DailyAlertWrites},
	} {
		if f.v <= 0 {
			bad(f.key, "must be greater than 0, got %d", f.v)
		}
	}
	for _, f := range []struct {
		key string
		v   time.Duration
	}{
		{"pacing.spacing_base", p.SpacingBase},
		{"pacing.recovery_interval", p.RecoveryInterval},
		{"ai.timeout", c.AI.Timeout},
	} {
		if f.v <= 0 {
			bad(f.key, "must be greater than 0, got %s", f.v)
		}
	}
	if p.SpacingJitter < 0 {
		bad("pacing.spacing_jitter", "must not be negative, got %s", p.SpacingJitter)
	}
	if !(p.SlowdownFactor > 0 && p.SlowdownFactor < 1) {
		bad("pacing.slowdown_factor", "must be between 0 and 1 (exclusive), got %g", p.SlowdownFactor)
	}
	if p.MinRatePerMin > 0 && p.MinRatePerMin > p.CeilingPerMin {
		bad("pacing.min_rate_per_min", "must not exceed pacing.ceiling_per_min (%d > %d)", p.MinRatePerMin, p.CeilingPerMin)
	}

	switch d := c.Presets.Default; {
	case d == "custom":
		bad("presets.default", "custom is set per group, not as the default; want one of %s", strings.Join(presetNames, ", "))
	case !slices.Contains(presetNames, d):
		bad("presets.default", "unknown preset; want one of %s", strings.Join(presetNames, ", "))
	}

	t := c.Scoring.Thresholds
	inRange := true
	for _, f := range []struct {
		key string
		v   float64
	}{
		{"scoring.thresholds.review", t.Review},
		{"scoring.thresholds.delete", t.Delete},
		{"scoring.thresholds.ban", t.Ban},
	} {
		if !inUnitInterval(f.v) {
			bad(f.key, "must be in (0, 1], got %g", f.v)
			inRange = false
		}
	}
	if inRange && !(t.Review < t.Delete && t.Delete <= t.Ban) {
		bad("scoring.thresholds", "want review < delete <= ban, got %g, %g, %g", t.Review, t.Delete, t.Ban)
	}
	signals := make([]string, 0, len(c.Scoring.Weights))
	for s := range c.Scoring.Weights {
		signals = append(signals, s)
	}
	slices.Sort(signals)
	for _, s := range signals {
		if w := c.Scoring.Weights[s]; math.IsNaN(w) || math.IsInf(w, 0) {
			bad("scoring.weights."+s, "must be a finite number")
		}
	}

	errs = append(errs, c.AI.validate(names)...)
	return errors.Join(errs...)
}

func (a AI) validate(names keyNames) []error {
	var errs []error
	bad := func(key, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: "+format, append([]any{names.of(key)}, args...)...))
	}
	knownProvider := a.Provider == "" || slices.Contains(providers, a.Provider)
	if !knownProvider {
		bad("ai.provider", "unknown provider; want one of %s, or leave it empty to turn AI off", strings.Join(providers, ", "))
	}
	if !slices.Contains(structuredOutputs, a.StructuredOutput) {
		bad("ai.structured_output", "unknown mode; want one of %s", strings.Join(structuredOutputs, ", "))
	}
	if !inUnitInterval(a.SpamConfidence) {
		bad("ai.spam_confidence", "must be in (0, 1], got %g", a.SpamConfidence)
	}
	if !envVarName.MatchString(a.APIKeyEnv) {
		bad("ai.api_key_env", "want the name of an environment variable in upper case, such as AI_API_KEY (not the key itself)")
	}
	if a.BaseURL != "" {
		if u, err := url.Parse(a.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			bad("ai.base_url", "want an http:// or https:// URL with a host")
		}
	}
	if a.Provider == "" || !knownProvider {
		return errs
	}
	if a.Model == "" {
		bad("ai.model", "required when an AI provider is set (in the file or AI_MODEL)")
	}
	if a.Provider == "openai_compatible" && a.BaseURL == "" {
		bad("ai.base_url", "required for provider openai_compatible (in the file or AI_BASE_URL)")
	}
	if (a.Provider == "anthropic" || a.Provider == "google") && a.APIKey == "" && envVarName.MatchString(a.APIKeyEnv) {
		errs = append(errs, fmt.Errorf("%s: required for provider %s; set the API key in this environment variable (named by ai.api_key_env)", a.APIKeyEnv, a.Provider))
	}
	return errs
}

func inUnitInterval(v float64) bool { return v > 0 && v <= 1 }

// warnings names each pacing limit raised above its default: that is a manual operator
// decision that raises the risk of a WhatsApp ban (DESIGN.md §10.3).
func (p Pacing) warnings() []string {
	var w []string
	for _, f := range []struct {
		key      string
		v, deflt int
	}{
		{"pacing.ceiling_per_min", p.CeilingPerMin, defaultCeilingPerMin},
		{"pacing.burst", p.Burst, defaultBurst},
	} {
		if f.v > f.deflt {
			w = append(w, fmt.Sprintf("%s is %d, above the default %d: this raises the risk of a WhatsApp ban; "+
				"raise it only in small steps, with measurements from your own number", f.key, f.v, f.deflt))
		}
	}
	return w
}
