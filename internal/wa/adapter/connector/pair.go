package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/logging"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// DefaultAbout is the bot's profile "about" text, set at pairing (DESIGN.md §8.5, §9.7): the bot
// is silent in groups, so a removed member finds the appeal path on its profile.
const DefaultAbout = "Group protection assistant. Removed by mistake? Message me 'appeal'."

// PairOptions configures Pair.
type PairOptions struct {
	// Phone, in international format (digits, optionally with + and spaces), selects pairing by
	// 8-character code. Empty means QR pairing.
	Phone string
	// About is the profile text to set after pairing; empty means DefaultAbout.
	About string
	// ShowQR receives each QR code to display (QR pairing).
	ShowQR func(code string, expires time.Duration)
	// ShowCode receives the pairing code to type into the phone (code pairing).
	ShowCode func(code string)
	// Lease and Owner, when set, hold the new bot's lease from just before the device is saved
	// until pairing is done with it, so a running connector doesn't connect the same session at
	// the same time (StreamReplaced). A number whose lease another process holds is refused.
	Lease Lease
	Owner string
	Log   zerolog.Logger
}

// ErrPairTimeout means the login connection closed before the QR code was scanned or the
// pairing code entered: the codes ran out (after about 160 s) or the connection dropped, and
// pairing must start again.
var ErrPairTimeout = errors.New("pair: timed out before the code was used")

// Pair links a new bot number to the whatsmeow store by QR or pairing code, then sets its about
// text and its "who can add me to groups" privacy setting to everyone (DESIGN.md §13.6 step 2).
// It sends no message. It returns the new bot's JID.
func Pair(ctx context.Context, container *sqlstore.Container, opts PairOptions) (wa.JID, error) {
	phone, err := pairPhone(opts.Phone)
	if err != nil {
		return "", err
	}
	dev := container.NewDevice()
	cli := whatsmeow.NewClient(dev, waLog.Zerolog(opts.Log.With().Str("module", "whatsmeow").Logger()))
	var hold *pairHold
	if opts.Lease != nil {
		hold = &pairHold{lease: opts.Lease, owner: opts.Owner, clk: clock.Real{}, log: opts.Log, lost: cli.Disconnect}
		// whatsmeow calls this just before it saves the device, the moment a Manager could see it.
		cli.PrePairCallback = func(j types.JID, _, _ string) bool { return hold.take(ctx, jid(j)) }
		// Deferred before Disconnect, so it runs after it: the lease outlives this client's session.
		defer hold.release(context.WithoutCancel(ctx))
	}
	qr, err := cli.GetQRChannel(ctx)
	if err != nil {
		return "", fmt.Errorf("pair: QR channel: %w", err)
	}
	if err := cli.ConnectContext(ctx); err != nil {
		return "", fmt.Errorf("pair: connect: %w", err)
	}
	defer cli.Disconnect()

	codeSent := false
	for item := range qr {
		switch {
		case item.Event == "code" && phone == "":
			if opts.ShowQR != nil {
				opts.ShowQR(item.Code, item.Timeout)
			}
		case item.Event == "code" && !codeSent:
			codeSent = true
			code, err := cli.PairPhone(ctx, phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
			if err != nil {
				return "", fmt.Errorf("pair: request pairing code: %w", err)
			}
			if opts.ShowCode != nil {
				opts.ShowCode(code)
			}
		case item.Event == "code":
		case item == whatsmeow.QRChannelSuccess:
			return finishPair(ctx, cli, opts, hold)
		case item == whatsmeow.QRChannelTimeout:
			return "", ErrPairTimeout
		case item.Event == "error" && hold != nil && hold.err() != nil:
			return "", fmt.Errorf("pair: refused: %w", hold.err())
		case item.Event == "error":
			return "", fmt.Errorf("pair: %w", item.Error)
		default:
			return "", fmt.Errorf("pair: %s", item.Event)
		}
	}
	return "", errors.New("pair: QR channel closed before pairing finished")
}

// finishPair sets the profile of a just-paired bot. Pair's deferred calls then disconnect the
// client and only after that give up the lease held since before the save.
func finishPair(ctx context.Context, cli *whatsmeow.Client, opts PairOptions, hold *pairHold) (wa.JID, error) {
	if cli.Store.ID == nil {
		return "", errors.New("pair: paired, but the device has no JID")
	}
	bot := jid(*cli.Store.ID)
	if hold != nil && hold.held() != bot {
		return bot, errors.New("pair: paired, but its lease is not held, so its profile was not set; set it by pairing again later")
	}
	if !cli.WaitForConnection(30 * time.Second) {
		return bot, errors.New("pair: paired, but the reconnect after pairing timed out; set the profile by pairing again later")
	}
	return bot, setupProfile(ctx, cli, opts.About)
}

// profileSetter is the part of the client setupProfile needs.
type profileSetter interface {
	SetStatusMessage(ctx context.Context, status types.SetStatusInput) error
	SetPrivacySetting(ctx context.Context, name types.PrivacySettingType, value types.PrivacySetting) (types.PrivacySettings, error)
}

// setupProfile sets the about text and lets anyone add the bot to groups (onboarding step 2).
// Both are profile settings, not messages, so they are done here rather than through the action
// queue, which paces messages and group writes.
func setupProfile(ctx context.Context, cli profileSetter, about string) error {
	if about == "" {
		about = DefaultAbout
	}
	if err := cli.SetStatusMessage(ctx, types.SetStatusInput{Text: &about}); err != nil {
		return fmt.Errorf("set about text: %w", err)
	}
	if _, err := cli.SetPrivacySetting(ctx, types.PrivacySettingTypeGroupAdd, types.PrivacySettingAll); err != nil {
		return fmt.Errorf("set group-add privacy: %w", err)
	}
	return nil
}

// pairPhone returns the digits of a pairing phone number; empty stays empty (QR pairing).
func pairPhone(s string) (string, error) {
	phone := digits(s)
	if s != "" && (len(phone) < 8 || len(phone) > 15) {
		return "", fmt.Errorf("pair: %s is not an international phone number", logging.Phone(s))
	}
	return phone, nil
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
