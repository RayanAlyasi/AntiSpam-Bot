//go:build integration

package main

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
)

func TestClosePoolWithHeldConnection(t *testing.T) {
	pool := storetest.Pool(t)
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Release) // a role that overran shutdown still holds this

	start := time.Now()
	closePool(pool, 500*time.Millisecond, zerolog.Nop())
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("closePool took %s with a held connection, want about the 500ms grace (exit must stay bounded)", took)
	}
}
