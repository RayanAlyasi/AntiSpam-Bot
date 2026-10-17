package main

import (
	"context"
	"flag"
	"io"
	"time"

	"github.com/mdp/qrterminal/v3"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/alert"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter/connector"
)

// pairCmd runs `antispam pair`: it links a new bot number by QR code or, with -phone, by an
// 8-character pairing code, then sets the bot's about text and lets anyone add it to groups
// (DESIGN.md §2.4, §8.5, §13.6). It sends no message. It returns the process exit code.
func pairCmd(ctx context.Context, args []string, getenv func(string) string, out io.Writer) int {
	fs := flag.NewFlagSet("antispam pair", flag.ContinueOnError)
	fs.SetOutput(out)
	phone := fs.String("phone", "", "pair by code for this international number (e.g. +966501234567) instead of a QR code")
	about := fs.String("about", connector.DefaultAbout, "profile about text to set after pairing")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		l := zerolog.New(out)
		l.Error().Err(errPairUsage).Msg("bad command line")
		return 2
	}
	cfg, logs, ok := loadConfig(getenv, out)
	if !ok {
		return 1
	}
	log := logs.For("wa")
	pool, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error().Err(err).Msg("connect to database")
		return 1
	}
	defer pool.Close()
	if _, err := store.Migrate(ctx, pool); err != nil {
		log.Error().Err(err).Msg("database schema")
		return 1
	}
	container, sqlDB, err := connector.OpenStore(ctx, cfg.DatabaseURL, logs.For("whatsmeow"))
	if err != nil {
		log.Error().Err(err).Msg("open whatsmeow store")
		return 1
	}
	defer func() { _ = sqlDB.Close() }()

	bot, err := connector.Pair(ctx, container, connector.PairOptions{
		Phone: *phone,
		About: *about,
		ShowQR: func(code string, expires time.Duration) {
			log.Info().Dur("expires_in", expires).Msg("scan this QR code in WhatsApp: Settings → Linked devices → Link a device")
			qrterminal.GenerateHalfBlock(code, qrterminal.L, out)
		},
		ShowCode: func(code string) {
			log.Info().Str("code", code).Msg("enter this code in WhatsApp: Linked devices → Link with phone number")
		},
		Lease: pairLease{store.NewBots(pool)},
		Owner: leaseOwner(),
		Log:   log,
	})
	if err != nil {
		log.Error().Err(err).Msg("pairing failed")
		return 1
	}
	log.Info().Str("bot", alert.MaskJID(bot)).Msg("paired; start the bot (or let the running connector pick it up within 10 s)")
	return 0
}
