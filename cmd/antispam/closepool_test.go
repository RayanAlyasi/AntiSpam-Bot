package main

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"
)

// blockingPool stands in for a pgxpool whose connections are never returned.
type blockingPool struct{ release chan struct{} }

func (p blockingPool) Close() { <-p.release }

type closedPool struct{}

func (closedPool) Close() {}

func TestClosePoolIsBounded(t *testing.T) {
	tests := []struct {
		name string
		held bool // a connection is never returned
		want time.Duration
	}{
		{"returns as soon as the pool closes", false, 0},
		{"gives up after the grace while a connection is held", true, time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// The channel must be made inside the bubble, or blocking on it isn't durable.
				var pool interface{ Close() } = closedPool{}
				if tt.held {
					pool = blockingPool{release: make(chan struct{})}
				}
				start := time.Now()
				closePool(pool, time.Second, zerolog.Nop())
				if took := time.Since(start); took != tt.want {
					t.Errorf("closePool returned after %s, want %s (a held connection must not block exit)", took, tt.want)
				}
				if p, ok := pool.(blockingPool); ok {
					close(p.release) // let the background Close finish inside the bubble
				}
			})
		})
	}
}
