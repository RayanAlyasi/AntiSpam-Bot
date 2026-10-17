// Command antispam is the bot binary. It runs every role (connector, workers, api) in one
// process by default (DESIGN.md §4.1). For now it only loads its configuration and exits.
package main

import (
	"context"
	"io"
	"os"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/config"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(context.Background(), os.Getenv, os.Stderr))
	}
	os.Exit(run(os.Getenv, os.Stderr))
}

// run loads the configuration and returns the process exit code.
func run(getenv func(string) string, logOut io.Writer) int {
	logger := zerolog.New(logOut).With().Timestamp().Logger()
	cfg, err := config.Load(getenv)
	if err != nil {
		logger.Error().Err(err).Msg("load config")
		return 1
	}
	logger = logger.Level(cfg.LogLevel)
	logger.Info().Int("operators", len(cfg.OperatorPhones)).Msg("config loaded; no roles are implemented yet, exiting")
	return 0
}
