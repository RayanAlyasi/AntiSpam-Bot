// Command antispam is the bot binary. It runs every role (connector, workers, api) in one
// process by default, or the subset named by -roles (DESIGN.md §4.1), and stops them all cleanly
// on SIGTERM or SIGINT within -shutdown-timeout.
//
// On start it connects to DATABASE_URL, refuses a database migrated by a newer release, and
// applies any pending migrations. With -check-config it only validates the configuration.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/riverqueue/river"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/app/fastlane"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/config"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/logging"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/jobs"
)

// defaultShutdownTimeout stays under Docker's default 10 s stop grace period, so the process
// exits on its own before it would be killed.
const defaultShutdownTimeout = 8 * time.Second

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(context.Background(), os.Getenv, os.Stderr))
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if len(os.Args) > 1 && os.Args[1] == "pair" {
		code := pairCmd(ctx, os.Args[2:], os.Getenv, os.Stderr)
		stop()
		os.Exit(code)
	}
	if len(os.Args) > 1 && os.Args[1] == "corpus" {
		code := corpusCmd(ctx, os.Args[2:], os.Getenv, os.Stdin, os.Stdout, os.Stderr)
		stop()
		os.Exit(code)
	}
	if len(os.Args) > 1 && os.Args[1] == "replay" {
		code := replayCmd(ctx, os.Args[2:], os.Getenv, os.Stdout, os.Stderr)
		stop()
		os.Exit(code)
	}
	os.Exit(cli(ctx, os.Args[1:], os.Getenv, os.Stderr))
}

// options are the command-line flags.
type options struct {
	checkConfig     bool
	roles           []string
	shutdownTimeout time.Duration
}

// cli parses args and runs the bot until ctx is cancelled. It returns the process exit code.
func cli(ctx context.Context, args []string, getenv func(string) string, logOut io.Writer) int {
	fs := flag.NewFlagSet("antispam", flag.ContinueOnError)
	fs.SetOutput(logOut)
	checkConfig := fs.Bool("check-config", false, "validate the configuration and exit")
	roles := fs.String("roles", strings.Join(allRoles, ","), "comma-separated roles to run: "+strings.Join(allRoles, ", "))
	shutdownTimeout := fs.Duration("shutdown-timeout", defaultShutdownTimeout, "how long roles get to stop after SIGTERM")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts := options{checkConfig: *checkConfig, shutdownTimeout: *shutdownTimeout}
	var err error
	if opts.roles, err = parseRoles(*roles); err != nil || opts.shutdownTimeout <= 0 || fs.NArg() > 0 {
		if err == nil {
			err = errors.New("-shutdown-timeout must be positive and no arguments are accepted")
		}
		l := zerolog.New(logOut)
		l.Error().Err(err).Msg("bad command line")
		return 2
	}
	if opts.checkConfig {
		return run(getenv, logOut)
	}
	return serve(ctx, opts, getenv, logOut)
}

// run loads and validates the configuration (-check-config) and returns the exit code.
func run(getenv func(string) string, logOut io.Writer) int {
	cfg, logs, ok := loadConfig(getenv, logOut)
	if !ok {
		return 1
	}
	log := logs.Base()
	log.Info().Int("operators", len(cfg.OperatorPhones)).Bool("ai", cfg.AI.Enabled()).Msg("config loaded")
	return 0
}

// loadConfig loads the configuration and builds the loggers from it, logging any warnings.
func loadConfig(getenv func(string) string, logOut io.Writer) (config.Config, *logging.Loggers, bool) {
	cfg, err := config.Load(getenv)
	if err != nil {
		l := zerolog.New(logOut).With().Timestamp().Logger()
		l.Error().Err(err).Msg("load config")
		return config.Config{}, nil, false
	}
	logs := logging.New(logOut, cfg.LogLevel, cfg.LogModules)
	for _, w := range cfg.Warnings {
		l := logs.For("config")
		l.Warn().Msg(w)
	}
	return cfg, logs, true
}

// serve connects to Postgres, migrates the schema and runs the chosen roles until ctx is
// cancelled.
func serve(ctx context.Context, opts options, getenv func(string) string, logOut io.Writer) int {
	cfg, logs, ok := loadConfig(getenv, logOut)
	if !ok {
		return 1
	}
	log := logs.Base()
	log.Info().Strs("roles", opts.roles).Int("operators", len(cfg.OperatorPhones)).Bool("ai", cfg.AI.Enabled()).Msg("config loaded")

	pool, err := store.Open(ctx, withPoolFloor(cfg.DatabaseURL, minPoolConns))
	if err != nil {
		log.Error().Err(err).Msg("connect to database")
		return 1
	}
	defer closePool(pool, poolCloseGrace, log)
	res, err := store.Migrate(ctx, pool)
	if err != nil {
		log.Error().Err(err).Msg("database schema")
		return 1
	}
	storeLog := logs.For("store")
	storeLog.Info().Int64("from", res.From).Int64("to", res.To).Int("applied", res.Applied).Msg("schema up to date")

	// The connector role reports here how many bot numbers are connected; /readyz reads it.
	var connectedBots atomic.Int64
	reg := api.NewRegistry()
	wa, lids := newWAStack(pool, getenv, logs, &connectedBots)
	wa.pacing = cfg.Pacing
	metrics, err := fastlane.NewMetrics(reg)
	if err != nil {
		log.Error().Err(err).Msg("metrics")
		return 1
	}
	key, ok := checkHashKey(ctx, cfg, pool, log)
	if !ok {
		return 1
	}
	var fl *fastlaneStack
	if key != nil && slices.Contains(opts.roles, roleWorkers) {
		if fl, err = newFastlane(cfg, key, pool, getenv, logs, wa, lids, reg, metrics); err != nil {
			log.Error().Err(err).Msg("detection fast lane")
			return 1
		}
		wa.moderate, wa.chat = fl.lanes, fl.chat
		if bl := fl.chat.deps.Blacklist; bl != nil {
			wa.moderate = blacklistSink{next: fl.lanes, bl: bl}
		}
		wa.hooks = &groupHooks{
			bl: fl.chat.deps.Blacklist, onboard: chatOnboarding{sink: fl.chat},
			bans: fl.reviews.Queue.Bans, persons: fl.reviews.Queue.Persons, admins: fl.chat.deps.Admins,
			groups: groupModes{groups: wa.groups, settings: fl.settings},
			reads:  &intervalBudget{clock: wa.clk, every: blacklistReadInterval},
			events: make(chan any, hookBacklog), log: logs.For("groups"),
		}
	}
	var roles []role
	for _, name := range opts.roles {
		switch name {
		case roleConnector:
			roles = append(roles, wa.connectorRole(cfg.DatabaseURL, reg, lids))
		case roleWorkers:
			registerWA := registerWAJobs(pool, logs.For("inbox"))
			register := func(w *river.Workers, p *[]*river.PeriodicJob) error {
				if err := registerWA(w, p); err != nil {
					return err
				}
				if fl != nil {
					return fl.registerJobs(w, p)
				}
				return nil
			}
			jobsLog := logs.For("jobs").Hook(fastlane.CallerHook{})
			jc, err := jobs.New(pool, jobs.Options{
				Work: true, Logger: jobsLog, Register: register,
				ErrorHandler: fastlane.JobErrors{Log: jobsLog, Metrics: metrics},
			})
			if err != nil {
				log.Error().Err(err).Msg("job client")
				return 1
			}
			roles = append(roles, workersRole(jc, opts.shutdownTimeout), wa.inboxRole())
			if fl != nil {
				roles = append(roles, fl.role())
			}
		case roleAPI:
			h := api.OpsHandler(reg, api.Readiness{
				PingDB:        pool.Ping,
				ConnectedBots: func() int { return int(connectedBots.Load()) },
			}, logs.For("api"))
			roles = append(roles, apiRole(cfg.HTTPAddr, h, opts.shutdownTimeout, logs.For("api")))
		}
	}
	if err := runRoles(ctx, roles, opts.shutdownTimeout, log); err != nil {
		log.Error().Err(err).Msg("stopped with an error")
		return 1
	}
	log.Info().Msg("stopped")
	return 0
}

// poolCloseGrace bounds how long the exit waits for database connections to be returned.
const poolCloseGrace = time.Second

// closePool closes pool, but waits at most grace: pgxpool's Close blocks until every connection
// is returned, and a role that overran the shutdown budget may still hold one. Without the bound
// the process would never exit on its own (F-008).
func closePool(pool interface{ Close() }, grace time.Duration, log zerolog.Logger) {
	done := make(chan struct{})
	go func() {
		pool.Close()
		close(done)
	}()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		log.Warn().Dur("grace", grace).Msg("database connections still in use; exiting without waiting for them")
	}
}

// workersRole runs River's job workers and periodic jobs.
func workersRole(jc *jobs.Client, shutdownTimeout time.Duration) role {
	return role{name: roleWorkers, run: func(ctx context.Context) error {
		// River stops through Stop below, not by cancelling its start context: a cancelled start
		// context leaves its notifier retrying the listener connection until Stop gives up.
		if err := jc.Start(context.WithoutCancel(ctx)); err != nil {
			return err
		}
		<-ctx.Done()
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		return jc.Stop(stopCtx)
	}}
}

// apiRole serves HTTP on addr: for now the operational endpoints (/metrics, /healthz, /readyz).
func apiRole(addr string, h http.Handler, shutdownTimeout time.Duration, log zerolog.Logger) role {
	return role{name: roleAPI, run: func(ctx context.Context) error {
		ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("listen on %s: %w", addr, err)
		}
		srv := &http.Server{
			Handler:           h,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       2 * time.Minute,
		}
		errc := make(chan error, 1)
		go func() { errc <- srv.Serve(ln) }()
		log.Info().Str("addr", ln.Addr().String()).Msg("http listening")
		select {
		case err := <-errc:
			return fmt.Errorf("http server: %w", err)
		case <-ctx.Done():
		}
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(stopCtx); err != nil {
			return fmt.Errorf("http shutdown: %w", err)
		}
		return nil
	}}
}
