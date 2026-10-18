package connector

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// errLeaseBusy is why a pairing is refused: another process holds the number's lease, so
// that process has the number connected (or did until moments ago).
var errLeaseBusy = errors.New("another running connector holds this number's lease; stop it, or wait 30 s if it just stopped, then pair again")

// pairHold holds a new bot's lease from just before whatsmeow saves the device until pairing is
// done with it. A running Manager connects every saved device whose lease it can take, so
// without the hold it could connect the bot between the save and the profile setup, while the
// pairing client is still online (StreamReplaced).
type pairHold struct {
	lease Lease
	owner string
	clk   clock.Clock
	log   zerolog.Logger
	// lost is called when a renewal fails: the pairing client must go offline, because the lease
	// may pass to a Manager any moment.
	lost func()

	mu      sync.Mutex
	bot     wa.JID
	refused error         // why take refused the pairing, if it did
	stop    chan struct{} // closed by release
	done    chan struct{} // closed when the renew loop has exited
}

// take acquires bot's lease and renews it every DefaultRenewEvery until release. False means the
// pairing must be refused (see err).
func (h *pairHold) take(ctx context.Context, bot wa.JID) bool {
	ok, err := h.lease.Acquire(ctx, bot, h.owner, DefaultLeaseTTL)
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case err != nil:
		h.refused = fmt.Errorf("take lease: %w", err)
		return false
	case !ok:
		h.refused = errLeaseBusy
		return false
	}
	h.bot, h.stop, h.done = bot, make(chan struct{}), make(chan struct{})
	go h.renew(context.WithoutCancel(ctx), bot, h.stop, h.done)
	return true
}

func (h *pairHold) renew(ctx context.Context, bot wa.JID, stop, done chan struct{}) {
	defer close(done)
	for {
		select {
		case <-stop:
			return
		case <-h.clk.After(DefaultRenewEvery):
		}
		ok, err := h.lease.Renew(ctx, bot, h.owner, DefaultLeaseTTL)
		if err == nil && ok {
			continue
		}
		h.log.Error().Err(err).Str("bot", mask(bot)).Msg("lost the pairing lease; disconnecting the pairing client")
		if h.lost != nil {
			h.lost()
		}
		return
	}
}

// held is the bot whose lease is held, or "".
func (h *pairHold) held() wa.JID {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.bot
}

// err is why take refused the pairing, or nil.
func (h *pairHold) err() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.refused
}

// release stops renewing and gives the lease up. The pairing client must be offline by then. It
// is safe to call when nothing is held, and more than once.
func (h *pairHold) release(ctx context.Context) {
	h.mu.Lock()
	bot, stop, done := h.bot, h.stop, h.done
	h.bot, h.stop, h.done = "", nil, nil
	h.mu.Unlock()
	if bot == "" {
		return
	}
	close(stop)
	<-done
	if err := h.lease.Release(ctx, bot, h.owner); err != nil {
		h.log.Error().Err(err).Str("bot", mask(bot)).Msg("release pairing lease")
	}
}
