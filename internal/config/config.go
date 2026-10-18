package config

import (
	"fmt"
	"net"
	"strings"

	"github.com/rs/zerolog"
)

// Config is the instance configuration. Every field has a safe default (Defaults), so the bot
// starts with no environment and no config file at all.
type Config struct {
	DatabaseURL    string                   // DATABASE_URL
	PublicURL      string                   // PUBLIC_URL: where the website is reachable
	HTTPAddr       string                   // HTTP_ADDR: listen address for the website, API, /metrics and health (default :8080)
	OperatorPhones []string                 // OPERATOR_PHONES: comma-separated phone numbers with the operator role
	LogLevel       zerolog.Level            // LOG_LEVEL: debug, info, warn or error (default info)
	LogModules     map[string]zerolog.Level // LOG_LEVEL module=level entries after the global level; nil when none
	HashKey        string                   // HASH_KEY: keys the phone-number hashes; required to run the bot (NumberHashKey)

	Pacing  Pacing  `yaml:"pacing"`
	Presets Presets `yaml:"presets"`
	Scoring Scoring `yaml:"scoring"`
	AI      AI      `yaml:"ai"`

	// Warnings are non-fatal notes about risky settings, for the caller to log at start-up.
	Warnings []string
}

// Load merges the defaults, the optional config file (CONFIG_FILE, or config.yaml in the working
// directory) and the environment, read through getenv (os.Getenv in production, a map in
// tests). Environment variables win over the file. The result is validated: any error means the
// bot must refuse to start. No error or warning contains the AI API key.
func Load(getenv func(string) string) (Config, error) {
	cfg := Defaults()
	cfg.DatabaseURL = strings.TrimSpace(getenv("DATABASE_URL"))
	cfg.PublicURL = strings.TrimSpace(getenv("PUBLIC_URL"))
	cfg.HashKey = strings.TrimSpace(getenv("HASH_KEY"))
	if addr := strings.TrimSpace(getenv("HTTP_ADDR")); addr != "" {
		if _, _, err := net.SplitHostPort(addr); err != nil {
			return Config{}, fmt.Errorf("HTTP_ADDR %q: want host:port or :port", addr)
		}
		cfg.HTTPAddr = addr
	}
	for p := range strings.SplitSeq(getenv("OPERATOR_PHONES"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			cfg.OperatorPhones = append(cfg.OperatorPhones, p)
		}
	}
	var err error
	if cfg.LogLevel, cfg.LogModules, err = parseLogLevel(getenv("LOG_LEVEL")); err != nil {
		return Config{}, err
	}
	if err := loadFile(&cfg, getenv); err != nil {
		return Config{}, err
	}
	names, err := applyAIEnv(&cfg.AI, getenv)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.validate(names); err != nil {
		return Config{}, err
	}
	cfg.Warnings = cfg.Pacing.warnings()
	return cfg, nil
}
