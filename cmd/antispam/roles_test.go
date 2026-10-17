package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"
)

func TestParseRoles(t *testing.T) {
	tests := []struct {
		raw     string
		want    []string
		wantErr string
	}{
		{"connector,workers,api", allRoles, ""},
		{"api", []string{"api"}, ""},
		{" api , workers ", []string{"workers", "api"}, ""},
		{"api,connector", []string{"connector", "api"}, ""},
		{"", nil, "unknown role"},
		{"web", nil, "unknown role"},
		{"api,", nil, "unknown role"},
		{"api,api", nil, "twice"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := parseRoles(tt.raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseRoles(%q) error %v, want %q", tt.raw, err, tt.wantErr)
				}
				return
			}
			if err != nil || !slices.Equal(got, tt.want) {
				t.Errorf("parseRoles(%q) = %v, %v; want %v", tt.raw, got, err, tt.want)
			}
		})
	}
}

// waits returns a role that blocks until cancelled, then takes stopIn to shut down.
func waits(name string, stopIn time.Duration) role {
	return role{name: name, run: func(ctx context.Context) error {
		<-ctx.Done()
		time.Sleep(stopIn)
		return nil
	}}
}

func TestRunRoles(t *testing.T) {
	errBoom := errors.New("boom")
	tests := []struct {
		name     string
		roles    []role
		signal   bool // cancel the parent context after a minute, like SIGTERM
		wantErr  error
		wantText string
		maxTime  time.Duration
	}{
		{
			name: "SIGTERM stops every role", roles: []role{waits("a", time.Second), waits("b", 2*time.Second)},
			signal: true, maxTime: time.Minute + 2*time.Second,
		},
		{name: "a failing role stops the others", roles: []role{
			waits("a", 0),
			{name: "b", run: func(context.Context) error { time.Sleep(time.Second); return errBoom }},
		}, wantErr: errBoom, wantText: "role b", maxTime: time.Second},
		{name: "a role returning early is an error", roles: []role{
			waits("a", 0), {name: "b", run: func(context.Context) error { return nil }},
		}, wantText: "stopped unexpectedly", maxTime: 0},
		{name: "a panicking role is a failure", roles: []role{
			waits("a", 0), {name: "b", run: func(context.Context) error { panic("bad") }},
		}, wantText: "panic: bad", maxTime: 0},
		{
			name: "a stuck role hits the shutdown timeout", roles: []role{waits("a", 0), waits("stuck", time.Hour)},
			signal: true, wantErr: errShutdownTimeout, wantText: "stuck still running", maxTime: time.Minute + 5*time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// Let a stuck role finish in fake time, so the bubble ends with no goroutines left.
				defer time.Sleep(2 * time.Hour)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if tt.signal {
					time.AfterFunc(time.Minute, cancel)
				}
				start := time.Now()
				err := runRoles(ctx, tt.roles, 5*time.Second, zerolog.Nop())
				if took := time.Since(start); took > tt.maxTime {
					t.Errorf("runRoles took %s, want at most %s (shutdown must be bounded)", took, tt.maxTime)
				}
				if tt.wantErr == nil && tt.wantText == "" {
					if err != nil {
						t.Fatalf("runRoles: %v, want a clean stop", err)
					}
					return
				}
				if err == nil {
					t.Fatal("runRoles returned nil, want an error")
				}
				if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
					t.Errorf("error %v, want %v", err, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantText) {
					t.Errorf("error %q lacks %q", err, tt.wantText)
				}
			})
		})
	}
}

func TestCLIRejectsBadFlags(t *testing.T) {
	for _, args := range [][]string{{"-roles", "web"}, {"-shutdown-timeout", "0s"}, {"extra"}, {"-nope"}} {
		var out strings.Builder
		if code := cli(context.Background(), args, func(string) string { return "" }, &out); code != 2 {
			t.Errorf("cli(%q) = %d, want 2 (a typo must not start the bot with defaults)", args, code)
		}
	}
}

func TestServeWithoutDatabaseFails(t *testing.T) {
	var out strings.Builder
	code := cli(context.Background(), nil, func(string) string { return "" }, &out)
	if code != 1 || !strings.Contains(out.String(), "DATABASE_URL is not set") {
		t.Errorf("cli() without DATABASE_URL = %d, log %q; want 1 naming DATABASE_URL", code, out.String())
	}
}

func TestCheckConfig(t *testing.T) {
	var out strings.Builder
	env := map[string]string{"LOG_LEVEL": "info,store=debug"}
	if code := cli(context.Background(), []string{"-check-config"}, func(k string) string { return env[k] }, &out); code != 0 {
		t.Fatalf("-check-config = %d, want 0; log %s", code, out.String())
	}
	if !strings.Contains(out.String(), "config loaded") {
		t.Errorf("log %q lacks config loaded", out.String())
	}
}
