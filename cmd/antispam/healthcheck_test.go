package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthzURL(t *testing.T) {
	tests := []struct {
		addr    string
		want    string
		wantErr bool
	}{
		{addr: "", want: "http://127.0.0.1:8080/healthz"},
		{addr: ":8080", want: "http://127.0.0.1:8080/healthz"},
		{addr: " :9090 ", want: "http://127.0.0.1:9090/healthz"},
		{addr: "0.0.0.0:8080", want: "http://127.0.0.1:8080/healthz"},
		{addr: "[::]:8080", want: "http://127.0.0.1:8080/healthz"},
		{addr: "127.0.0.1:8081", want: "http://127.0.0.1:8081/healthz"},
		{addr: "[::1]:8080", want: "http://[::1]:8080/healthz"},
		{addr: "localhost:8080", want: "http://localhost:8080/healthz"},
		{addr: "8080", wantErr: true},
		{addr: "127.0.0.1:", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			got, err := healthzURL(tt.addr)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("healthzURL(%q) = %q, want an error (no usable port)", tt.addr, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("healthzURL(%q) = %q, %v; want %q (the probe must hit the local listener)", tt.addr, got, err, tt.want)
			}
		})
	}
}

func TestHealthcheck(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantCode int
	}{
		{name: "healthy", status: http.StatusOK, wantCode: 0},
		{name: "unhealthy", status: http.StatusServiceUnavailable, wantCode: 1},
		{name: "not found", status: http.StatusNotFound, wantCode: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var path string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()
			_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
			env := map[string]string{"HTTP_ADDR": ":" + port}
			var out bytes.Buffer
			if got := healthcheck(t.Context(), func(k string) string { return env[k] }, &out); got != tt.wantCode {
				t.Fatalf("healthcheck() = %d, want %d for a %d answer; log: %s", got, tt.wantCode, tt.status, out.String())
			}
			if path != "/healthz" {
				t.Fatalf("probed %q, want /healthz", path)
			}
		})
	}
}

func TestHealthcheckFailsWhenNothingListens(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	env := map[string]string{"HTTP_ADDR": addr}
	var out bytes.Buffer
	if got := healthcheck(t.Context(), func(k string) string { return env[k] }, &out); got != 1 {
		t.Fatalf("healthcheck() = %d, want 1 when nothing listens (a dead bot must be reported unhealthy)", got)
	}
	if !strings.Contains(out.String(), "unreachable") {
		t.Fatalf("log %q doesn't say the endpoint is unreachable", out.String())
	}
}

func TestHealthcheckRejectsBadAddr(t *testing.T) {
	env := map[string]string{"HTTP_ADDR": "nonsense"}
	var out bytes.Buffer
	if got := healthcheck(t.Context(), func(k string) string { return env[k] }, &out); got != 1 {
		t.Fatalf("healthcheck() = %d, want 1 for HTTP_ADDR=nonsense", got)
	}
}
