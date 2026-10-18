package connector

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/logging"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Auto-pairing pace. A pairing code lives about 160 s (whatsmeow closes the login connection
// when its QR codes run out), and each request shows a notification on the phone, so a code
// nobody enters is replaced after a pause that doubles up to a cap, and only so many times.
const (
	autoPairFirstWait = 30 * time.Second
	autoPairMaxWait   = 10 * time.Minute
	// DefaultAutoPairAttempts is how many codes AutoPair requests before it gives up (about an
	// hour and a half of codes with the waits above).
	DefaultAutoPairAttempts = 10
)

// PairFunc runs one pairing attempt; Pair bound to the whatsmeow store is the real one. It
// returns ErrPairTimeout when the code expired unused.
type PairFunc func(ctx context.Context, opts PairOptions) (wa.JID, error)

// DeviceLister lists the paired devices in the whatsmeow store (Backend has it).
type DeviceLister interface {
	Devices(ctx context.Context) ([]Device, error)
}

// AutoPairOptions configures AutoPair.
type AutoPairOptions struct {
	// Phone is the bot's number in international format (PAIR_PHONE). Empty turns AutoPair off.
	Phone string
	About string
	// Lease and Owner are passed to Pair. Owner must differ from the Manager's, so the
	// Manager doesn't connect the new bot while pairing still holds it.
	Lease Lease
	Owner string
	// Attempts bounds the codes requested; 0 means DefaultAutoPairAttempts.
	Attempts int
	Clock    clock.Clock
	Log      zerolog.Logger
}

// AutoPair pairs a bot by phone-number code when the store has no paired device yet: it logs
// each code, requests a new one when a code expires unused, and stops once pairing succeeds, a
// device shows up some other way (antispam pair), Attempts codes went unused, or ctx ends. With
// a device already paired, or no Phone, it does nothing. It sends no message. The error is only
// for a Phone that isn't an international number.
func AutoPair(ctx context.Context, devices DeviceLister, pair PairFunc, opts AutoPairOptions) error {
	if opts.Phone == "" {
		return nil
	}
	if _, err := pairPhone(opts.Phone); err != nil {
		return err
	}
	if opts.Attempts <= 0 {
		opts.Attempts = DefaultAutoPairAttempts
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	log := opts.Log.With().Str("module", "autopair").Stringer("phone", logging.Phone(opts.Phone)).Logger()
	wait := autoPairFirstWait
	for codes := 0; ; {
		devs, err := devices.Devices(ctx)
		switch {
		case err != nil && ctx.Err() != nil:
			return nil
		case err != nil:
			log.Warn().Err(err).Dur("retry_in", wait).Msg("list paired devices before pairing; retrying")
		case len(devs) > 0 && codes > 0:
			log.Info().Msg("a bot was paired another way; stopped requesting pairing codes")
			return nil
		case len(devs) > 0:
			return nil // already paired: PAIR_PHONE has nothing to do
		case codes >= opts.Attempts:
			log.Warn().Int("attempts", opts.Attempts).Msg("no pairing code was used; stopped requesting codes. Restart the bot for new ones, or run antispam pair")
			return nil
		default:
			codes++
			bot, err := pair(ctx, PairOptions{
				Phone: opts.Phone,
				About: opts.About,
				ShowCode: func(code string) {
					log.Info().Str("code", code).Msgf("pairing code %s: enter it on the bot's phone in WhatsApp → Linked devices → Link with phone number", code)
				},
				Lease: opts.Lease,
				Owner: opts.Owner,
				Log:   opts.Log,
			})
			switch {
			case err == nil:
				log.Info().Stringer("bot", logging.JID(bot)).Msg("paired by PAIR_PHONE; the connector picks the bot up within 10 s. You can remove PAIR_PHONE now")
				return nil
			case bot != "":
				// Linked, but the profile step after it failed: pairing again can't fix that.
				log.Warn().Err(err).Stringer("bot", logging.JID(bot)).Msg("paired by PAIR_PHONE, but the about text or group-add privacy wasn't set; set them on the phone. You can remove PAIR_PHONE now")
				return nil
			case ctx.Err() != nil:
				return nil
			case errors.Is(err, ErrPairTimeout):
				log.Info().Dur("next_code_in", wait).Msg("the pairing code expired unused or the connection dropped; a new code follows")
			default:
				log.Warn().Err(err).Dur("retry_in", wait).Msg("pairing by PAIR_PHONE failed; retrying")
			}
		}
		if err := opts.Clock.Sleep(ctx, wait); err != nil {
			return nil
		}
		wait = min(2*wait, autoPairMaxWait)
	}
}
