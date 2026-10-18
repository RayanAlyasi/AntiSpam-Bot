package connector

import (
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/rs/zerolog"
)

// TestPairHoldKeepsManagerOffNewDevice replays the interleaving that used to race: whatsmeow
// saves the new device, a Manager tick runs before the profile is set, and pairing finishes
// later. The tick must not connect the bot, however long the profile step takes, and the next
// tick after pairing lets the lease go must.
func TestPairHoldKeepsManagerOffNewDevice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		e.backend.devices = nil // nothing paired yet
		m := e.manager(t, "connector")
		var lost atomic.Bool
		hold := &pairHold{lease: e.lease, owner: "pairing", clk: e.clk, log: zerolog.Nop(), lost: func() { lost.Store(true) }}

		m.Tick(t.Context())
		if !hold.take(t.Context(), bot) { // whatsmeow's PrePairCallback, just before the save
			t.Fatalf("take refused: %v", hold.err())
		}
		synctest.Wait()                          // the renew loop is waiting on the clock
		e.backend.devices = []Device{{JID: bot}} // whatsmeow saves the device
		m.Tick(t.Context())                      // the tick that used to win the lease
		// The profile step runs past the lease TTL several times over; renewals keep the lease.
		for range 2 * int(DefaultLeaseTTL/DefaultRenewEvery) {
			e.clk.Advance(DefaultRenewEvery)
			synctest.Wait()
			m.Tick(t.Context())
		}
		if n := len(e.backend.sessions); n != 0 {
			t.Fatalf("manager opened %d sessions while pairing held the bot", n)
		}
		if lost.Load() {
			t.Fatal("hold reported a lost lease while renewing it")
		}

		hold.release(t.Context()) // Pair's deferred release, after the client disconnected
		m.Tick(t.Context())
		if e.backend.live() != 1 {
			t.Fatalf("manager didn't connect the bot after pairing released it (live sessions: %d)", e.backend.live())
		}
		hold.release(t.Context()) // a second release is harmless
	})
}

func TestPairHoldRefusesHeldNumber(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		m := e.manager(t, "connector")
		m.Tick(t.Context()) // the connector has the number connected
		hold := &pairHold{lease: e.lease, owner: "pairing", clk: e.clk, log: zerolog.Nop()}
		if hold.take(t.Context(), bot) {
			t.Fatal("take succeeded on a number another process holds")
		}
		if !errors.Is(hold.err(), errLeaseBusy) {
			t.Errorf("err = %v, want errLeaseBusy", hold.err())
		}
		hold.release(t.Context()) // nothing held: must not release the connector's lease
		if e.lease.owner[bot] != "connector" {
			t.Errorf("lease owner = %q, want the connector", e.lease.owner[bot])
		}
	})
}

func TestPairHoldLostLeaseDisconnects(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		var lost atomic.Bool
		hold := &pairHold{lease: e.lease, owner: "pairing", clk: e.clk, log: zerolog.Nop(), lost: func() { lost.Store(true) }}
		if !hold.take(t.Context(), bot) {
			t.Fatal(hold.err())
		}
		synctest.Wait() // the renew loop is waiting on the clock
		e.lease.steal(bot, "elsewhere")
		e.clk.Advance(DefaultRenewEvery)
		synctest.Wait()
		if !lost.Load() {
			t.Fatal("a failed renewal didn't take the pairing client offline")
		}
		hold.release(t.Context())
		if e.lease.owner[bot] != "elsewhere" {
			t.Errorf("release gave up a lease it no longer held (owner %q)", e.lease.owner[bot])
		}
	})
}
