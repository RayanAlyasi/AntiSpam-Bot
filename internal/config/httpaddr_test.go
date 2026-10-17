package config

import (
	"strings"
	"testing"
)

func TestHTTPAddr(t *testing.T) {
	tests := []struct {
		env     string
		want    string
		wantErr bool
	}{
		{"", DefaultHTTPAddr, false},
		{"   ", DefaultHTTPAddr, false},
		{":9090", ":9090", false},
		{"127.0.0.1:8080", "127.0.0.1:8080", false},
		{"[::1]:8080", "[::1]:8080", false},
		{"8080", "", true},
		{"localhost", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			t.Chdir(t.TempDir()) // no config.yaml
			cfg, err := Load(func(k string) string {
				if k == "HTTP_ADDR" {
					return tt.env
				}
				return ""
			})
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "HTTP_ADDR") {
					t.Fatalf("HTTP_ADDR=%q: error %v, want one naming HTTP_ADDR (a bad address must stop the bot)", tt.env, err)
				}
				return
			}
			if err != nil || cfg.HTTPAddr != tt.want {
				t.Errorf("HTTP_ADDR=%q: got %q, %v; want %q", tt.env, cfg.HTTPAddr, err, tt.want)
			}
		})
	}
}
