package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		wantCode int
		wantLog  string
	}{
		{name: "starts with no config and exits cleanly", env: map[string]string{}, wantCode: 0, wantLog: "config loaded"},
		{name: "bad config exits non-zero", env: map[string]string{"LOG_LEVEL": "loud"}, wantCode: 1, wantLog: "load config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if got := run(func(k string) string { return tt.env[k] }, &out); got != tt.wantCode {
				t.Fatalf("run() = %d, want %d; log: %s", got, tt.wantCode, out.String())
			}
			if !strings.Contains(out.String(), tt.wantLog) {
				t.Fatalf("log %q doesn't mention %q", out.String(), tt.wantLog)
			}
		})
	}
}
