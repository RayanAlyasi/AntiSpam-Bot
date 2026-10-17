package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/rs/zerolog"
)

const defaultLogLevel = zerolog.InfoLevel

var moduleName = regexp.MustCompile(`^[a-z0-9_-]+$`)

// parseLogLevel reads LOG_LEVEL: a global level, optionally followed by comma-separated
// module=level entries ("info,store=debug,wa=warn"). When the first element is already a
// module entry, the global level stays at the default. modules is nil when there are none.
func parseLogLevel(raw string) (global zerolog.Level, modules map[string]zerolog.Level, err error) {
	global = defaultLogLevel
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return global, nil, nil
	}
	for i, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		name, lvl, isModule := strings.Cut(entry, "=")
		if !isModule {
			if i > 0 {
				return 0, nil, fmt.Errorf("LOG_LEVEL entry %q: want module=level after the global level", entry)
			}
			if global, err = parseLevel(entry); err != nil {
				return 0, nil, fmt.Errorf("LOG_LEVEL %q: %w", entry, err)
			}
			continue
		}
		name, lvl = strings.TrimSpace(name), strings.TrimSpace(lvl)
		if !moduleName.MatchString(name) {
			return 0, nil, fmt.Errorf("LOG_LEVEL entry %q: module name must be lowercase letters, digits, _ or -", entry)
		}
		if _, dup := modules[name]; dup {
			return 0, nil, fmt.Errorf("LOG_LEVEL entry %q: module %s is set twice", entry, name)
		}
		l, err := parseLevel(lvl)
		if err != nil {
			return 0, nil, fmt.Errorf("LOG_LEVEL entry %q: %w", entry, err)
		}
		if modules == nil {
			modules = map[string]zerolog.Level{}
		}
		modules[name] = l
	}
	return global, modules, nil
}

// parseLevel accepts the four levels the bot offers, in any case.
func parseLevel(s string) (zerolog.Level, error) {
	switch lvl := strings.ToLower(s); lvl {
	case "debug", "info", "warn", "error":
		return zerolog.ParseLevel(lvl)
	default:
		return 0, errors.New("want debug, info, warn or error")
	}
}
