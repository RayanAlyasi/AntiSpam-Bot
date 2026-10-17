package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
)

func TestOpsEndpoints(t *testing.T) {
	dbOK := func(context.Context) error { return nil }
	dbDown := func(context.Context) error { return errors.New("connection refused") }
	bots := func(n int) func() int { return func() int { return n } }

	tests := []struct {
		name     string
		path     string
		ready    Readiness
		wantCode int
		wantBody map[string]any
	}{
		{"healthz needs nothing", "/healthz", Readiness{PingDB: dbDown}, 200, map[string]any{"status": "ok"}},
		{
			"ready", "/readyz",
			Readiness{PingDB: dbOK, ConnectedBots: bots(1)},
			200,
			map[string]any{"status": "ok", "database": "ok", "bots_connected": 1.0},
		},
		{
			"no bot connected", "/readyz",
			Readiness{PingDB: dbOK, ConnectedBots: bots(0)},
			503,
			map[string]any{"status": "unavailable", "database": "ok", "bots_connected": 0.0},
		},
		{
			"database down", "/readyz",
			Readiness{PingDB: dbDown, ConnectedBots: bots(2)},
			503,
			map[string]any{"status": "unavailable", "database": "unreachable", "bots_connected": 2.0},
		},
		{
			"nothing wired", "/readyz",
			Readiness{},
			503,
			map[string]any{"status": "unavailable", "database": "not configured", "bots_connected": 0.0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := OpsHandler(NewRegistry(), tt.ready, zerolog.Nop())
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, nil))
			if rec.Code != tt.wantCode {
				t.Errorf("%s = %d, want %d (Coolify restarts or routes on this)", tt.path, rec.Code, tt.wantCode)
			}
			var got map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("body %q is not JSON: %v", rec.Body, err)
			}
			for k, v := range tt.wantBody {
				if got[k] != v {
					t.Errorf("body[%q] = %v, want %v", k, got[k], v)
				}
			}
			if strings.Contains(rec.Body.String(), "connection refused") {
				t.Error("readiness body leaks the database error; it belongs in the logs")
			}
		})
	}
}

func TestMetricsServesRegisteredCollectors(t *testing.T) {
	reg := NewRegistry()
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: "antispam_test_total", Help: "test"})
	reg.MustRegister(c)
	c.Inc()
	rec := httptest.NewRecorder()
	OpsHandler(reg, Readiness{}, zerolog.Nop()).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("/metrics = %d, want 200", rec.Code)
	}
	for _, want := range []string{"antispam_test_total 1", "go_goroutines"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}
}

func TestOpsRejectsOtherMethods(t *testing.T) {
	rec := httptest.NewRecorder()
	OpsHandler(NewRegistry(), Readiness{}, zerolog.Nop()).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/healthz", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz = %d, want 405", rec.Code)
	}
}
