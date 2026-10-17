package config

import (
	"slices"
	"testing"

	"github.com/rs/zerolog"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		want      Config
		wantError bool
	}{
		{
			name: "no environment uses safe defaults",
			env:  map[string]string{},
			want: Config{LogLevel: zerolog.InfoLevel},
		},
		{
			name: "all variables",
			env: map[string]string{
				"DATABASE_URL":    "postgres://bot@db/antispam",
				"PUBLIC_URL":      "https://bot.example.org",
				"OPERATOR_PHONES": " +966500000001, ,+966500000002 ",
				"LOG_LEVEL":       "DEBUG",
			},
			want: Config{
				DatabaseURL:    "postgres://bot@db/antispam",
				PublicURL:      "https://bot.example.org",
				OperatorPhones: []string{"+966500000001", "+966500000002"},
				LogLevel:       zerolog.DebugLevel,
			},
		},
		{name: "unknown log level", env: map[string]string{"LOG_LEVEL": "verbose"}, wantError: true},
		{name: "trace is not offered", env: map[string]string{"LOG_LEVEL": "trace"}, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(func(k string) string { return tt.env[k] })
			if tt.wantError {
				if err == nil {
					t.Fatalf("Load() = %+v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got.DatabaseURL != tt.want.DatabaseURL || got.PublicURL != tt.want.PublicURL ||
				got.LogLevel != tt.want.LogLevel || !slices.Equal(got.OperatorPhones, tt.want.OperatorPhones) {
				t.Fatalf("Load() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
