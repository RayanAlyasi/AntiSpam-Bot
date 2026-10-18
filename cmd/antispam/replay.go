package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/app/fastlane"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/config"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/replay"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// replayCmd is `antispam replay` (DESIGN.md §18, F-089). It never talks to WhatsApp.
//
//	antispam replay -fixtures FILE [-weights FILE] [-compare FILE]
//	    replays a JSONL fixture through the full pipeline in memory and prints one verdict per
//	    message and a summary; with -compare, it replays again under a second weight file and
//	    lists the messages whose verdict changed.
//	antispam replay -shadow [-since 7d] [-group JID]
//	    lists what shadow mode would have done, from the detections in DATABASE_URL (read only).
//	antispam replay -inbox [-since 24h] [-group JID] [-weights FILE]
//	    re-runs the stored inbox (the last 72 h of received messages) through today's rules,
//	    corpus, weights and settings and prints what the bot would decide now (read only; needs
//	    HASH_KEY to match blacklisted numbers).
//
// It returns the exit code: 0 on success, 1 on an error, 2 on a bad command line.
func replayCmd(ctx context.Context, args []string, getenv func(string) string, out, logOut io.Writer) int {
	log := zerolog.New(logOut)
	fs := flag.NewFlagSet("antispam replay", flag.ContinueOnError)
	fs.SetOutput(logOut)
	fixtures := fs.String("fixtures", "", "JSONL fixture file to replay")
	weights := fs.String("weights", "", "weight file (YAML) to score with; default: the built-in weights")
	compare := fs.String("compare", "", "second weight file: replay again and list the changed verdicts")
	shadow := fs.Bool("shadow", false, "list stored shadow-mode detections from DATABASE_URL")
	inboxRows := fs.Bool("inbox", false, "re-run the stored inbox in DATABASE_URL through the current rules")
	since := new(time.Duration)
	*since = 7 * 24 * time.Hour
	fs.Func("since", "with -shadow or -inbox: how far back to look, e.g. 24h or 7d (default 7d)", func(v string) (err error) {
		*since, err = parseSince(v)
		return err
	})
	group := fs.String("group", "", "with -shadow or -inbox: only this group JID")
	limit := fs.Int("limit", 1000, "with -shadow or -inbox: the most rows to list")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	sources := 0
	for _, on := range []bool{*fixtures != "", *shadow, *inboxRows} {
		if on {
			sources++
		}
	}
	switch {
	case fs.NArg() > 0 || sources != 1 || *since <= 0 || *limit <= 0:
		log.Error().Msg("use exactly one of -fixtures FILE, -shadow or -inbox, with a positive -since and -limit")
		return 2
	case *compare != "" && *fixtures == "":
		log.Error().Msg("-compare works with -fixtures only")
		return 2
	case *inboxRows:
		if err := inboxReport(ctx, getenv, *weights, wa.JID(*group), *since, int32(min(*limit, 100000)), out); err != nil {
			log.Error().Err(err).Msg("inbox replay")
			return 1
		}
		return 0
	case *shadow:
		if err := shadowReport(ctx, getenv("DATABASE_URL"), wa.JID(*group), *since, int32(min(*limit, 100000)), out); err != nil {
			log.Error().Err(err).Msg("shadow report")
			return 1
		}
		return 0
	}
	if err := replayFixtures(*fixtures, *weights, *compare, out); err != nil {
		log.Error().Err(err).Msg("replay")
		return 1
	}
	return 0
}

// parseSince reads a duration, also in whole days ("7d"), which time.ParseDuration lacks.
func parseSince(v string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(v, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%q: want a positive number of days such as 7d", v)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(v)
}

func replayFixtures(path, weightsPath, comparePath string, out io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	recs, err := replay.Load(f)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	before, err := replayWith(recs, weightsPath)
	if err != nil {
		return err
	}
	if err := replay.Write(out, before); err != nil {
		return err
	}
	if comparePath == "" {
		return nil
	}
	after, err := replayWith(recs, comparePath)
	if err != nil {
		return err
	}
	changes := replay.Diff(before, after)
	if _, err := fmt.Fprintf(out, "-- %d changed with %s\n", len(changes), comparePath); err != nil {
		return err
	}
	for _, c := range changes {
		if _, err := fmt.Fprintf(out, "- %s\n+ %s\n", orDash(c.Before), orDash(c.After)); err != nil {
			return err
		}
	}
	_, err = fmt.Fprint(out, after.Summarize().String())
	return err
}

func replayWith(recs []replay.Record, weightsPath string) (replay.Report, error) {
	w := scoring.DefaultWeights()
	if weightsPath != "" {
		data, err := os.ReadFile(weightsPath)
		if err != nil {
			return replay.Report{}, err
		}
		if w, err = scoring.ParseWeights(data); err != nil {
			return replay.Report{}, fmt.Errorf("%s: %w", weightsPath, err)
		}
	}
	sim, err := replay.NewSim(replay.SimOptions{Weights: w})
	if err != nil {
		return replay.Report{}, err
	}
	return replay.Run(sim, recs)
}

// inboxReport replays the stored inbox read-only and prints the verdicts and a summary.
func inboxReport(ctx context.Context, getenv func(string) string, weightsPath string, group wa.JID, since time.Duration, limit int32, out io.Writer) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	key, err := cfg.NumberHashKey()
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		return errors.New("DATABASE_URL is not set")
	}
	w := scoring.DefaultWeights()
	if weightsPath != "" {
		data, err := os.ReadFile(weightsPath)
		if err != nil {
			return err
		}
		if w, err = scoring.ParseWeights(data); err != nil {
			return fmt.Errorf("%s: %w", weightsPath, err)
		}
	}
	pool, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	rep, err := fastlane.InboxReplay{
		Pool: pool, HashKey: key, Weights: w, Log: zerolog.Nop(), NewGroupMode: newGroupMode(cfg.NewGroups.AskAdmins),
	}.Run(ctx, group, time.Now().Add(-since), limit)
	if err != nil {
		return err
	}
	return replay.Write(out, rep)
}

// shadowReport prints one line per stored shadow detection: when, where, the route, what would
// have been done and why. It only reads.
func shadowReport(ctx context.Context, url string, group wa.JID, since time.Duration, limit int32, out io.Writer) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("DATABASE_URL is not set")
	}
	pool, err := store.Open(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	rows, err := db.New(pool).ListDetectionsSince(ctx, db.ListDetectionsSinceParams{
		Mode: "shadow", Since: time.Now().Add(-since), GroupJid: string(group), RowLimit: limit,
	})
	if err != nil {
		return fmt.Errorf("list shadow detections: %w", err)
	}
	var deletes, bans, reviews int
	for _, r := range rows {
		acts := planned(r.ActionPlan)
		for _, a := range acts {
			switch a {
			case "delete":
				deletes++
			case "ban":
				bans++
			case "review":
				reviews++
			}
		}
		if _, err := fmt.Fprintf(out, "%s %s %s route=%s source=%s would=%s score=%.2f why=%s\n", r.CreatedAt.UTC().Format(time.RFC3339),
			r.GroupJid, r.MsgID, r.Route, text(r.ConfidenceSource, "none"), orDash(strings.Join(acts, "+")), r.Score, why(r.Signals)); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(out, "-- %d shadow detections: would have deleted %d messages, banned %d people and asked admins %d times\n",
		len(rows), deletes, bans, reviews)
	return err
}

func planned(raw []byte) []string {
	var plan []pgstore.PlannedAction
	if json.Unmarshal(raw, &plan) != nil {
		return nil
	}
	out := make([]string, 0, len(plan))
	for _, p := range plan {
		out = append(out, p.Kind)
	}
	return out
}

// why lists the signal codes behind a detection: detections hold no message text (DESIGN.md §14).
func why(raw []byte) string {
	var s struct {
		Signals []struct {
			Code string `json:"code"` // policy.Signal has no tags; JSON keys match case-insensitively
		} `json:"signals"`
		Violations []string `json:"violations"`
		Reason     string   `json:"reason"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return "-"
	}
	var parts []string
	for _, sig := range s.Signals {
		if sig.Code != "" {
			parts = append(parts, sig.Code)
		}
	}
	parts = append(parts, s.Violations...)
	return orDash(strings.Join(parts, ","))
}

func text(t pgtype.Text, empty string) string {
	if !t.Valid || t.String == "" {
		return empty
	}
	return t.String
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
