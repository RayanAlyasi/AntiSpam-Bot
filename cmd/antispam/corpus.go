package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/config"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/corpusimport"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/corpusimport/pgimport"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
)

// corpusUsage is printed for a bad `antispam corpus` command line.
const corpusUsage = `usage:
  antispam corpus import <file|-> --scope instance|group:<jid> [--holdout 0.2] [--holdout-clusters FILE] [--allow-ham] [--dry-run]
  antispam corpus remove --import <id>
`

// corpusCmd is `antispam corpus` (DESIGN.md §7.8, F-147 to F-150): importing a labeled spam
// dataset into the confirmed-spam corpus, and undoing an import. It needs DATABASE_URL and
// HASH_KEY, and never talks to WhatsApp. `-` reads the dataset from stdin, which is how it runs
// in the distroless image: docker exec -i <container> /antispam corpus import - < file.
//
// It returns the exit code: 0 on success, 1 on an error, 2 on a bad command line.
func corpusCmd(ctx context.Context, args []string, getenv func(string) string, in io.Reader, out, logOut io.Writer) int {
	log := zerolog.New(logOut)
	if len(args) == 0 {
		_, _ = fmt.Fprint(logOut, corpusUsage) // the exit code already reports the error
		return 2
	}
	switch args[0] {
	case "import":
		return corpusImport(ctx, args[1:], getenv, in, out, log, logOut)
	case "remove":
		return corpusRemove(ctx, args[1:], getenv, out, log, logOut)
	}
	_, _ = fmt.Fprint(logOut, corpusUsage) // the exit code already reports the error
	return 2
}

func corpusImport(ctx context.Context, args []string, getenv func(string) string, in io.Reader, out io.Writer, log zerolog.Logger, logOut io.Writer) int {
	fs := flag.NewFlagSet("antispam corpus import", flag.ContinueOnError)
	fs.SetOutput(logOut)
	scope := fs.String("scope", "", "where the entries apply: instance, or group:<group JID>")
	holdout := fs.Float64("holdout", corpusimport.DefaultHoldout, "share of spam records held out for evaluation, split by cluster")
	holdoutFile := fs.String("holdout-clusters", "", "file listing the cluster_ids to hold out, one per line, instead of a random split")
	allowHam := fs.Bool("allow-ham", false, "add the ham records' fingerprints (never their text) to the group's allow-list")
	dryRun := fs.Bool("dry-run", false, "show what would happen without writing anything")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 || *scope == "" {
		log.Error().Msg("give exactly one dataset file (or - for stdin) and --scope")
		_, _ = fmt.Fprint(logOut, corpusUsage) // the exit code already reports the error
		return 2
	}
	sc, err := corpusimport.ParseScope(*scope)
	if err != nil {
		log.Error().Err(err).Msg("bad command line")
		return 2
	}
	var clusters []string
	if *holdoutFile != "" {
		if clusters, err = readLines(*holdoutFile); err != nil {
			log.Error().Err(err).Msg("read --holdout-clusters")
			return 1
		}
		if len(clusters) == 0 {
			log.Error().Str("file", *holdoutFile).Msg("--holdout-clusters lists no cluster")
			return 2
		}
	}
	cfg, err := config.Load(getenv)
	if err != nil {
		log.Error().Err(err).Msg("load config")
		return 1
	}
	key, err := cfg.NumberHashKey()
	if err != nil {
		log.Error().Err(err).Msg("HASH_KEY")
		return 1
	}
	opts := corpusimport.Options{
		Scope: sc, Holdout: *holdout, HoldoutClusters: clusters, DryRun: *dryRun, AllowHam: *allowHam,
		HashKey: key, Now: time.Now(),
	}
	if err := opts.Validate(); err != nil {
		log.Error().Err(err).Msg("bad command line")
		return 2
	}
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		log.Error().Msg("DATABASE_URL is not set")
		return 1
	}

	name := pos[0]
	src := in
	if name != "-" {
		f, err := os.Open(name)
		if err != nil {
			log.Error().Err(err).Msg("open dataset")
			return 1
		}
		defer func() { _ = f.Close() }()
		src = f
		opts.FileName = filepath.Base(name)
	} else {
		opts.FileName = "stdin"
	}
	data, err := io.ReadAll(io.LimitReader(src, corpusimport.MaxInput+1))
	if err != nil {
		log.Error().Err(err).Msg("read dataset")
		return 1
	}
	sum := sha256.Sum256(data)
	opts.FileSHA256, opts.Format = hex.EncodeToString(sum[:]), corpusimport.DetectFormat(data)
	recs, rejected, err := corpusimport.Parse(bytes.NewReader(data))
	if err != nil {
		log.Error().Err(err).Msg("read dataset")
		return 1
	}
	if len(recs) == 0 {
		for _, r := range rejected {
			_, _ = fmt.Fprintf(out, "rejected %s\n", r) // on the way to exit 1 anyway
		}
		log.Error().Int("rejected", len(rejected)).Msg("the dataset has no valid record; nothing was written")
		return 1
	}

	pool, err := openMigrated(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error().Err(err).Msg("database")
		return 1
	}
	defer pool.Close()
	rep, err := pgimport.Run(ctx, pool, recs, rejected, opts)
	if err != nil {
		log.Error().Err(err).Msg("import")
		return 1
	}
	if err := rep.Write(out); err != nil {
		log.Error().Err(err).Msg("write report")
		return 1
	}
	return 0
}

func corpusRemove(ctx context.Context, args []string, getenv func(string) string, out io.Writer, log zerolog.Logger, logOut io.Writer) int {
	fs := flag.NewFlagSet("antispam corpus remove", flag.ContinueOnError)
	fs.SetOutput(logOut)
	batch := fs.String("import", "", "the import batch ID the import printed")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	id, err := strconv.ParseInt(*batch, 10, 64)
	if err != nil || id <= 0 || fs.NArg() > 0 {
		log.Error().Msg("give the import batch ID: antispam corpus remove --import <id>")
		return 2
	}
	cfg, err := config.Load(getenv)
	if err != nil {
		log.Error().Err(err).Msg("load config")
		return 1
	}
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		log.Error().Msg("DATABASE_URL is not set")
		return 1
	}
	pool, err := openMigrated(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error().Err(err).Msg("database")
		return 1
	}
	defer pool.Close()
	rm, err := pgimport.Remove(ctx, pool, id)
	if err != nil {
		log.Error().Err(err).Msg("remove import")
		if errors.Is(err, pgimport.ErrNoBatch) {
			return 2
		}
		return 1
	}
	_, err = fmt.Fprintf(out, "removed import %d: %d corpus entries, %d hold-out records, %d allow-list fingerprints, %d learned rules\n",
		id, rm.Entries, rm.Holdout, rm.Allow, rm.Rules)
	if err != nil {
		return 1
	}
	return 0
}

// openMigrated connects and brings the schema up to date, as the bot does on start: an import
// can run before the bot ever started, and a database migrated by a newer release is refused.
func openMigrated(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := store.Open(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if _, err := store.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	return pool, nil
}

// parseInterspersed parses flags that may come before or after the positional arguments
// ("import data.jsonl --scope instance" as well as "import --scope instance data.jsonl"). A lone
// "-" is a positional argument (stdin).
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// readLines reads the non-blank lines of a file, trimmed.
func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			out = append(out, l)
		}
	}
	return out, sc.Err()
}
