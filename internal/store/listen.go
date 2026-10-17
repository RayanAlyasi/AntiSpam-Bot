package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

// RulesChannel is the NOTIFY channel the rules, fingerprints and number_blacklist triggers
// signal on (migrations 00030, 00031).
const RulesChannel = "rules_changed"

// closeTimeout bounds closing the LISTEN connection, so a dead server can't hang shutdown.
const closeTimeout = 5 * time.Second

// listenRetry is the wait before re-establishing a lost LISTEN connection.
const listenRetry = 2 * time.Second

// ListenRulesChanged holds one pooled connection on LISTEN rules_changed and sends a value on
// changes for every notification, until ctx ends. It also sends one after each (re)connect,
// because notifications sent while it was disconnected are lost, so the receiver reloads
// everything. Sends never block: changes should have a buffer of at least 1, and a pending
// value already means "reload". A lost connection is retried after a short wait.
func ListenRulesChanged(ctx context.Context, pool *pgxpool.Pool, clk clock.Clock, log zerolog.Logger, changes chan<- struct{}) error {
	signal := func() {
		select {
		case changes <- struct{}{}:
		default:
		}
	}
	for {
		err := listenOnce(ctx, pool, signal)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Warn().Err(err).Msg("rules LISTEN connection lost; reconnecting")
		if err := clk.Sleep(ctx, listenRetry); err != nil {
			return err
		}
	}
}

func listenOnce(ctx context.Context, pool *pgxpool.Pool, signal func()) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire listen connection: %w", err)
	}
	// A connection that has been LISTENing must not go back to the pool for other users.
	pc := conn.Hijack()
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
		defer cancel()
		_ = pc.Close(cctx)
	}()
	if _, err := pc.Exec(ctx, "LISTEN "+RulesChannel); err != nil {
		return fmt.Errorf("listen %s: %w", RulesChannel, err)
	}
	signal()
	for {
		n, err := pc.WaitForNotification(ctx)
		if err != nil {
			return fmt.Errorf("wait for %s: %w", RulesChannel, err)
		}
		if n == nil {
			return errors.New("wait for notification: no notification")
		}
		signal()
	}
}
