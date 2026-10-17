package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// The process roles (DESIGN.md §4.1). They all run in one process by default; -roles picks a
// subset, so they can be split across processes later (§15).
const (
	roleConnector = "connector"
	roleWorkers   = "workers"
	roleAPI       = "api"
)

var allRoles = []string{roleConnector, roleWorkers, roleAPI}

// parseRoles reads the -roles flag: a comma-separated subset of allRoles, returned in canonical
// order.
func parseRoles(raw string) ([]string, error) {
	seen := map[string]bool{}
	for r := range strings.SplitSeq(raw, ",") {
		r = strings.TrimSpace(r)
		if !slices.Contains(allRoles, r) {
			return nil, fmt.Errorf("-roles: unknown role %q (want a comma-separated list of %s)", r, strings.Join(allRoles, ", "))
		}
		if seen[r] {
			return nil, fmt.Errorf("-roles: %q is listed twice", r)
		}
		seen[r] = true
	}
	var out []string
	for _, r := range allRoles {
		if seen[r] {
			out = append(out, r)
		}
	}
	return out, nil
}

// role is one long-running part of the process. Run blocks until ctx is cancelled, then shuts
// down and returns nil; it returns early only on failure.
type role struct {
	name string
	run  func(ctx context.Context) error
}

// errShutdownTimeout means a role didn't return within the shutdown budget.
var errShutdownTimeout = errors.New("shutdown timed out")

// runRoles runs every role until ctx is cancelled (SIGTERM) or one of them fails, then cancels
// the rest and waits at most timeout for them to return. It returns the first role failure, or
// errShutdownTimeout naming the roles that didn't stop in time.
func runRoles(ctx context.Context, roles []role, timeout time.Duration, log zerolog.Logger) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		name string
		err  error
	}
	results := make(chan result, len(roles))
	running := map[string]bool{}
	for _, r := range roles {
		running[r.name] = true
		go func() {
			defer func() {
				if p := recover(); p != nil {
					results <- result{r.name, fmt.Errorf("panic: %v", p)}
				}
			}()
			results <- result{r.name, r.run(runCtx)}
		}()
		log.Info().Str("role", r.name).Msg("role started")
	}

	var firstErr error
	record := func(res result) {
		delete(running, res.name)
		if res.err != nil && !errors.Is(res.err, context.Canceled) {
			log.Error().Err(res.err).Str("role", res.name).Msg("role failed")
			if firstErr == nil {
				firstErr = fmt.Errorf("role %s: %w", res.name, res.err)
			}
			return
		}
		log.Info().Str("role", res.name).Msg("role stopped")
	}

	select {
	case <-ctx.Done():
		log.Info().Msg("shutting down")
	case res := <-results:
		record(res)
		if firstErr == nil {
			firstErr = fmt.Errorf("role %s: stopped unexpectedly", res.name)
		}
	}
	cancel()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for len(running) > 0 {
		select {
		case res := <-results:
			record(res)
		case <-deadline.C:
			left := make([]string, 0, len(running))
			for name := range running {
				left = append(left, name)
			}
			slices.Sort(left)
			return errors.Join(firstErr, fmt.Errorf("%w after %s: %s still running", errShutdownTimeout, timeout, strings.Join(left, ", ")))
		}
	}
	return firstErr
}
