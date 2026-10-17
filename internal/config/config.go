package config

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog"
)

// Config is the instance configuration. Every field has a safe default, so the bot starts
// with no environment set at all; later features add their settings here.
type Config struct {
	DatabaseURL    string        // DATABASE_URL
	PublicURL      string        // PUBLIC_URL: where the website is reachable
	OperatorPhones []string      // OPERATOR_PHONES: comma-separated phone numbers with the operator role
	LogLevel       zerolog.Level // LOG_LEVEL: debug, info, warn or error (default info)
}

// Load reads the configuration through getenv (os.Getenv in production, a map in tests).
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		DatabaseURL: strings.TrimSpace(getenv("DATABASE_URL")),
		PublicURL:   strings.TrimSpace(getenv("PUBLIC_URL")),
		LogLevel:    zerolog.InfoLevel,
	}
	for p := range strings.SplitSeq(getenv("OPERATOR_PHONES"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			cfg.OperatorPhones = append(cfg.OperatorPhones, p)
		}
	}
	if raw := strings.TrimSpace(getenv("LOG_LEVEL")); raw != "" {
		switch lvl := strings.ToLower(raw); lvl {
		case "debug", "info", "warn", "error":
			cfg.LogLevel, _ = zerolog.ParseLevel(lvl)
		default:
			return Config{}, fmt.Errorf("LOG_LEVEL %q: want debug, info, warn or error", raw)
		}
	}
	return cfg, nil
}
