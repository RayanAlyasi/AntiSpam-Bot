//go:build integration

package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
)

// lockedBuffer is a log sink safe to read while serve writes to it.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestServeRunsAllRolesAndStopsOnSignal(t *testing.T) {
	addr := freeAddr(t)
	env := map[string]string{"DATABASE_URL": storetest.URL(t, false), "HTTP_ADDR": addr}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out lockedBuffer
	done := make(chan int, 1)
	go func() { done <- cli(ctx, nil, func(k string) string { return env[k] }, &out) }()

	base := "http://" + addr
	deadline := time.Now().Add(time.Minute)
	for {
		if code, _ := get(t, base+"/healthz"); code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("/healthz never answered; log:\n%s", out.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if code, body := get(t, base+"/readyz"); code != http.StatusServiceUnavailable || !strings.Contains(body, `"database":"ok"`) {
		t.Errorf("/readyz = %d %s, want 503 with the database ok (no bot is connected yet)", code, body)
	}
	if code, body := get(t, base+"/metrics"); code != http.StatusOK || !strings.Contains(body, "go_goroutines") {
		t.Errorf("/metrics = %d, want 200 with Go metrics", code)
	}
	for _, want := range []string{`"role":"connector"`, `"role":"workers"`, `"role":"api"`, "schema up to date"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("log lacks %s:\n%s", want, out.String())
		}
	}

	cancel() // what SIGTERM does in main
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code %d after SIGTERM, want 0; log:\n%s", code, out.String())
		}
	case <-time.After(defaultShutdownTimeout + 5*time.Second):
		t.Fatalf("serve didn't stop within the shutdown timeout; log:\n%s", out.String())
	}
}

func TestServeRefusesNewerSchema(t *testing.T) {
	url := storetest.URL(t, true)
	pool, err := store.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	newest, err := store.NewestMigration()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), "INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)", newest+5); err != nil {
		t.Fatal(err)
	}
	pool.Close()

	var out lockedBuffer
	env := map[string]string{"DATABASE_URL": url, "HTTP_ADDR": freeAddr(t)}
	code := cli(context.Background(), nil, func(k string) string { return env[k] }, &out)
	if code != 1 {
		t.Fatalf("exit code %d on a newer schema, want 1 (never run on an unknown schema)", code)
	}
	if !strings.Contains(out.String(), "only knows up to") {
		t.Errorf("log %q doesn't say which version is needed", out.String())
	}
}

func TestServeSubsetOfRoles(t *testing.T) {
	addr := freeAddr(t)
	env := map[string]string{"DATABASE_URL": storetest.URL(t, true), "HTTP_ADDR": addr}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out lockedBuffer
	if code := cli(ctx, []string{"-roles", "workers"}, func(k string) string { return env[k] }, &out); code != 0 {
		t.Fatalf("exit code %d, want 0; log:\n%s", code, out.String())
	}
	if strings.Contains(out.String(), `"role":"api"`) || strings.Contains(out.String(), `"role":"connector"`) {
		t.Errorf("-roles workers started other roles:\n%s", out.String())
	}
	if code, _ := get(t, "http://"+addr+"/healthz"); code != 0 {
		t.Errorf("-roles workers served HTTP (status %d), want no listener", code)
	}
}
