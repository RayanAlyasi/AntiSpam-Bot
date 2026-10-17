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
	// Lease and Owner, when set, take the new bot's lease before the profile is set, so a
	// running connector doesn't connect the same session at the same time (StreamReplaced).
	Lease Lease
	Owner string
	Log   zerolog.Logger
}

// Pair links a new bot number to the whatsmeow store by QR or pairing code, then sets its about
// text and its "who can add me to groups" privacy setting to everyone (DESIGN.md §13.6 step 2).
// It sends no message. It returns the new bot's JID.
func Pair(ctx context.Context, container *sqlstore.Container, opts PairOptions) (wa.JID, error) {
	phone := digits(opts.Phone)
	if opts.Phone != "" && (len(phone) < 8 || len(phone) > 15) {
		return "", fmt.Errorf("pair: %q is not an international phone number", opts.Phone)
	}
	dev := container.NewDevice()
	cli := whatsmeow.NewClient(dev, waLog.Zerolog(opts.Log.With().Str("module", "whatsmeow").Logger()))
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
			return finishPair(ctx, cli, opts)
		case item.Event == "error":
			return "", fmt.Errorf("pair: %w", item.Error)
		default:
			return "", fmt.Errorf("pair: %s", item.Event)
		}
	}
	return "", errors.New("pair: QR channel closed before pairing finished")
}

func finishPair(ctx context.Context, cli *whatsmeow.Client, opts PairOptions) (wa.JID, error) {
	if cli.Store.ID == nil {
		return "", errors.New("pair: paired, but the device has no JID")
	}
	bot := jid(*cli.Store.ID)
	if opts.Lease != nil {
		ok, err := opts.Lease.Acquire(ctx, bot, opts.Owner, DefaultLeaseTTL)
		if err != nil {
			return bot, fmt.Errorf("pair: take lease: %w", err)
		}
		if !ok {
			opts.Log.Warn().Str("bot", mask(bot)).Msg("paired; a running connector already owns the bot, so its profile was not set")
			return bot, nil
		}
		defer func() {
			// Disconnect before giving the lease up, so the connector never connects while this
			// client is still online (StreamReplaced).
			cli.Disconnect()
			if err := opts.Lease.Release(context.WithoutCancel(ctx), bot, opts.Owner); err != nil {
				opts.Log.Error().Err(err).Msg("release pairing lease")
			}
		}()
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

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
