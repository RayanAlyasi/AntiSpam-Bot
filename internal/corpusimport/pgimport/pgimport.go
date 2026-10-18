// Package pgimport stores a dataset import in Postgres, measures its hold-out recall with the
// real detection pipeline, and undoes an import (DESIGN.md §7.8).
package pgimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/app/fastlane"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/corpusimport"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
)

// StartedBy is what import_batches records as the importer: the command line.
const StartedBy = "cli"

// ErrNoBatch means there is no import with that ID.
var ErrNoBatch = errors.New("no such import")

// Run imports recs (DESIGN.md §7.8): it plans the import (normalize, fingerprint, merge
// duplicates, split the hold-out by cluster), drops what the live corpus already holds in the
// target scope and what an earlier import held out, replays the hold-out and the ham records through the fast lane against the
// corpus as it will be once the batch is stored, then, unless opts.DryRun, stores the batch in
// one transaction: its import_batches row, the corpus entries, the hold-out's eval_corpus rows
// and, with opts.AllowHam, the ham records' allow-list fingerprints. Ham text is never written.
func Run(ctx context.Context, pool *pgxpool.Pool, recs []corpusimport.Record, rejected []corpusimport.Rejection, opts corpusimport.Options) (corpusimport.Report, error) {
	plan, err := corpusimport.NewPlan(recs, opts)
	if err != nil {
		return corpusimport.Report{}, err
	}
	var group pgtype.Text
	if !opts.Scope.Instance() {
		group = pgtype.Text{String: string(opts.Scope.Group), Valid: true}
	}
	q := db.New(pool)
	rows, err := q.ListCorpusForDedupe(ctx, db.ListCorpusForDedupeParams{Now: opts.Now, GroupJid: group})
	if err != nil {
		return corpusimport.Report{}, fmt.Errorf("list corpus: %w", err)
	}
	held, err := q.ListEvalForDedupe(ctx)
	if err != nil {
		return corpusimport.Report{}, fmt.Errorf("list hold-out: %w", err)
	}
	existing := make([]corpusimport.Existing, 0, len(rows)+len(held))
	for _, r := range rows {
		existing = append(existing, corpusimport.Existing{TextNorm: r.TextNorm, MediaSHA256: r.MediaSha256, PHashes: r.Phashes})
	}
	for _, r := range held {
		existing = append(existing, corpusimport.Existing{TextNorm: r.TextNorm, MediaSHA256: r.MediaSha256, PHashes: r.Phashes})
	}
	plan.DropExisting(existing)
	rep := corpusimport.NewReport(plan, rejected, opts.DryRun)
	if err := evaluate(ctx, pool, plan, opts, &rep); err != nil {
		return rep, err
	}
	if opts.DryRun {
		return rep, nil
	}
	err = store.InTx(ctx, pool, func(tx pgx.Tx) error {
		id, err := write(ctx, tx, plan, opts)
		if err != nil {
			return err
		}
		rep.BatchID = id
		stats, err := json.Marshal(statsOf(rep))
		if err != nil {
			return fmt.Errorf("import stats: %w", err)
		}
		return db.New(tx).FinishImportBatch(ctx, db.FinishImportBatchParams{
			ID: id, Stats: stats, FinishedAt: pgtype.Timestamptz{Time: opts.Now, Valid: true},
		})
	})
	if err != nil {
		rep.BatchID = 0
		return rep, fmt.Errorf("store import: %w", err)
	}
	return rep, nil
}

// evaluate replays the hold-out (recall) and the ham records (false positives) in memory.
func evaluate(ctx context.Context, pool *pgxpool.Pool, plan corpusimport.Plan, opts corpusimport.Options, rep *corpusimport.Report) error {
	if len(plan.Holdout)+len(plan.Ham) == 0 {
		return nil
	}
	group := fastlane.EvalGroup
	if !opts.Scope.Instance() {
		group = opts.Scope.Group
	}
	extra := make([]string, 0, len(plan.Corpus))
	for _, it := range plan.Corpus {
		extra = append(extra, it.Entry.TextNorm)
	}
	msgs := make([]fastlane.LabeledMessage, 0, len(plan.Holdout)+len(plan.Ham))
	for _, it := range plan.Holdout {
		msgs = append(msgs, fastlane.LabeledMessage{Envelope: it.Envelope, Label: corpusimport.LabelSpam})
	}
	for _, it := range plan.Ham {
		msgs = append(msgs, fastlane.LabeledMessage{Envelope: it.Envelope, Label: corpusimport.LabelHam})
	}
	res, err := fastlane.HoldoutReplay{Pool: pool, HashKey: opts.HashKey, Log: zerolog.Nop()}.Run(ctx, group, extra, msgs)
	if err != nil {
		return fmt.Errorf("replay the hold-out: %w", err)
	}
	s := res.Summarize()
	rep.Recall = corpusimport.Recall{Total: s.Spam, Deleted: s.SpamDeleted, ToAdmins: s.SpamToAdmins, Missed: s.SpamMissed}
	rep.HamCaught = s.HamDeleted + s.HamToAdmins
	return nil
}

func write(ctx context.Context, tx pgx.Tx, plan corpusimport.Plan, opts corpusimport.Options) (int64, error) {
	q := db.New(tx)
	scope := "instance"
	var group pgtype.Text
	if !opts.Scope.Instance() {
		scope, group = "group", pgtype.Text{String: string(opts.Scope.Group), Valid: true}
	}
	id, err := q.InsertImportBatch(ctx, db.InsertImportBatchParams{
		FileName: opts.FileName, FileSha256: opts.FileSHA256, Format: opts.Format, Scope: scope, GroupJid: group,
		StartedBy: StartedBy, StartedAt: opts.Now,
	})
	if err != nil {
		return 0, fmt.Errorf("insert import batch: %w", err)
	}
	batch := pgtype.Int8{Int64: id, Valid: true}
	for _, it := range plan.Corpus {
		e := it.Entry
		e.ImportBatchID = &id
		if _, err := store.InsertCorpusEntry(ctx, tx, e); err != nil {
			return 0, fmt.Errorf("record on line %d: %w", it.Record.Line, err)
		}
	}
	for _, it := range plan.Holdout {
		if err := q.InsertEvalCorpus(ctx, db.InsertEvalCorpusParams{
			ImportBatchID: batch, ClusterKey: it.Cluster, TextNorm: it.Entry.TextNorm,
			MediaSha256: nonNil(it.Entry.MediaSHA256), Phashes: nonNil(it.Entry.PHashes),
			Category: it.Record.Category, Kind: it.Kind, Language: it.Language, AddedAt: opts.Now,
		}); err != nil {
			return 0, fmt.Errorf("hold-out record on line %d: %w", it.Record.Line, err)
		}
	}
	if opts.AllowHam {
		for _, it := range plan.Ham {
			if it.FP.Empty() {
				continue
			}
			if err := q.InsertImportAllow(ctx, db.InsertImportAllowParams{
				GroupJid: string(opts.Scope.Group), Simhash: int64(it.FP.SimHash), Minhash: it.FP.MinHashBytes(),
				Tokens: int32(it.FP.Tokens), ImportBatchID: batch,
			}); err != nil {
				return 0, fmt.Errorf("allow-list ham record on line %d: %w", it.Record.Line, err)
			}
		}
	}
	return id, nil
}

// nonNil returns an empty slice for nil, for the NOT NULL array columns.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// stats is the import_batches.stats document: the report's counts, no text.
type stats struct {
	Read            int `json:"read"`
	Rejected        int `json:"rejected"`
	Spam            int `json:"spam"`
	Ham             int `json:"ham"`
	Imported        int `json:"imported"`
	MergedInFile    int `json:"merged_in_file"`
	MergedExisting  int `json:"merged_existing"`
	Clusters        int `json:"clusters"`
	Holdout         int `json:"holdout"`
	HoldoutClusters int `json:"holdout_clusters"`
	RecallDeleted   int `json:"recall_deleted"`
	RecallToAdmins  int `json:"recall_to_admins"`
	RecallMissed    int `json:"recall_missed"`
	HamCaught       int `json:"ham_caught"`
}

func statsOf(r corpusimport.Report) stats {
	return stats{
		Read: r.Read, Rejected: len(r.Rejected), Spam: r.Spam, Ham: r.Ham, Imported: r.Imported,
		MergedInFile: r.MergedInFile, MergedExisting: r.MergedExisting, Clusters: r.Clusters,
		Holdout: r.Holdout, HoldoutClusters: r.HoldoutClusters, RecallDeleted: r.Recall.Deleted,
		RecallToAdmins: r.Recall.ToAdmins, RecallMissed: r.Recall.Missed, HamCaught: r.HamCaught,
	}
}

// Removed counts what Remove deleted.
type Removed struct {
	Entries, Holdout, Allow int
	Rules                   int // rules learned only from the batch's entries
}

// Remove undoes one import (§7.8 "Imports can be undone"): in one transaction it deletes the
// rules learned only from the batch's entries, the entries themselves (their confirmations and
// sources go with them), its hold-out rows and its allow-list fingerprints, and marks the batch
// removed. The batch row stays, as the record that the import happened and was undone; removing
// it again removes nothing. A running bot drops the entries from its index on its next corpus
// refresh (fastlane.CorpusRefresh).
func Remove(ctx context.Context, pool *pgxpool.Pool, batchID int64) (Removed, error) {
	var out Removed
	err := store.InTx(ctx, pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		b, err := q.GetImportBatch(ctx, batchID)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %d", ErrNoBatch, batchID)
		}
		if err != nil {
			return fmt.Errorf("get import %d: %w", batchID, err)
		}
		if b.RemovedAt.Valid {
			return nil
		}
		batch := pgtype.Int8{Int64: batchID, Valid: true}
		steps := []struct {
			n   *int
			del func(context.Context, pgtype.Int8) (int64, error)
			of  string
		}{
			{&out.Rules, q.DeleteImportRules, "learned rules"},
			{&out.Entries, q.DeleteImportEntries, "corpus entries"},
			{&out.Holdout, q.DeleteImportEval, "hold-out rows"},
			{&out.Allow, q.DeleteImportAllow, "allow-list fingerprints"},
		}
		for _, s := range steps {
			n, err := s.del(ctx, batch)
			if err != nil {
				return fmt.Errorf("delete the import's %s: %w", s.of, err)
			}
			*s.n = int(n)
		}
		if _, err := q.MarkImportRemoved(ctx, db.MarkImportRemovedParams{ID: batchID, RemovedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}}); err != nil {
			return fmt.Errorf("mark import %d removed: %w", batchID, err)
		}
		return nil
	})
	if err != nil {
		return Removed{}, err
	}
	return out, nil
}
