package config

import (
	"maps"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		raw         string
		wantGlobal  zerolog.Level
		wantModules map[string]zerolog.Level
		wantErr     string // substring; "" means success
	}{
		{raw: "", wantGlobal: zerolog.InfoLevel},
		{raw: "DEBUG", wantGlobal: zerolog.DebugLevel},
		{
			raw: "info,store=debug,wa=warn", wantGlobal: zerolog.InfoLevel,
			wantModules: map[string]zerolog.Level{"store": zerolog.DebugLevel, "wa": zerolog.WarnLevel},
		},
		{
			raw: " error , store = DEBUG ", wantGlobal: zerolog.ErrorLevel,
			wantModules: map[string]zerolog.Level{"store": zerolog.DebugLevel},
		},
		{
			raw: "store=debug", wantGlobal: zerolog.InfoLevel,
			wantModules: map[string]zerolog.Level{"store": zerolog.DebugLevel},
		},
		{
			raw: "warn,river_queue-2=error", wantGlobal: zerolog.WarnLevel,
			wantModules: map[string]zerolog.Level{"river_queue-2": zerolog.ErrorLevel},
		},
		{raw: "verbose", wantErr: `LOG_LEVEL "verbose"`},
		{raw: "info,debug", wantErr: `LOG_LEVEL entry "debug"`},
		{raw: "info,", wantErr: "LOG_LEVEL entry"},
		{raw: ",store=debug", wantErr: "LOG_LEVEL"},
		{raw: "info,Store=debug", wantErr: `LOG_LEVEL entry "Store=debug"`},
		{raw: "info,st ore=debug", wantErr: "LOG_LEVEL entry"},
		{raw: "info,=debug", wantErr: "LOG_LEVEL entry"},
		{raw: "info,store=", wantErr: "LOG_LEVEL entry"},
		{raw: "info,store=trace", wantErr: `LOG_LEVEL entry "store=trace"`},
		{raw: "info,store=debug,store=warn", wantErr: "set twice"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			global, modules, err := parseLogLevel(tt.raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseLogLevel(%q) error = %v, want one containing %q (a bad LOG_LEVEL must stop start-up)", tt.raw, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLogLevel(%q) error = %v, want nil", tt.raw, err)
			}
			if global != tt.wantGlobal || !maps.Equal(modules, tt.wantModules) || (modules == nil) != (tt.wantModules == nil) {
				t.Fatalf("parseLogLevel(%q) = %v, %v; want %v, %v", tt.raw, global, modules, tt.wantGlobal, tt.wantModules)
			}
		})
	}
}

func TestLoadSetsLogModules(t *testing.T) {
	t.Chdir(t.TempDir())
	env := map[string]string{"LOG_LEVEL": "debug,wa=warn"}
	cfg, err := Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.LogLevel != zerolog.DebugLevel || cfg.LogModules["wa"] != zerolog.WarnLevel || len(cfg.LogModules) != 1 {
		t.Fatalf("Load() LogLevel, LogModules = %v, %v; want debug, map[wa:warn]", cfg.LogLevel, cfg.LogModules)
	}
}
