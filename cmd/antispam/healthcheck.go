package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// defaultHTTPAddr is where the API and health endpoints listen when HTTP_ADDR is unset.
const defaultHTTPAddr = ":8080"

// healthcheck probes the local /healthz endpoint and returns the exit code: 0 when it answers
// 200, 1 otherwise. It's the container healthcheck (`antispam healthcheck`), because the
// distroless image has no shell or curl.
func healthcheck(ctx context.Context, getenv func(string) string, logOut io.Writer) int {
	logger := zerolog.New(logOut).With().Timestamp().Str("cmd", "healthcheck").Logger()
	url, err := healthzURL(getenv("HTTP_ADDR"))
	if err != nil {
		logger.Error().Err(err).Msg("bad listen address")
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		logger.Error().Err(err).Msg("build request")
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Error().Err(err).Str("url", url).Msg("unreachable")
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		logger.Error().Str("url", url).Int("status", resp.StatusCode).Msg("unhealthy")
		return 1
	}
	return 0
}

// healthzURL turns a listen address such as ":8080", "0.0.0.0:8080" or "[::]:8080" into the
// loopback URL of its /healthz endpoint.
func healthzURL(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = defaultHTTPAddr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("HTTP_ADDR %q: %w", addr, err)
	}
	if port == "" {
		return "", fmt.Errorf("HTTP_ADDR %q: no port", addr)
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz", nil
}
