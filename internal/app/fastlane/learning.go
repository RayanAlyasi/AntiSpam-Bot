package fastlane

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/corpus"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/neardup"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	reviewpg "github.com/RayanAlyasi/AntiSpam-Bot/internal/review/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// CorpusLearning is review.Corpus on Postgres (F-113/F-114, DESIGN.md §20 v0.1 "where known spam
// comes from"): a message an admin confirms as spam becomes a group-scoped, active corpus entry
// (redacted text, fingerprints, hashed numbers), and an overturn retracts it. Storage is wp-k's
// review/pgstore.Corpus (the one implementation and its corpus_sources message → entry table,
// pattern confirmations for promotion, exact SimHash and media allow-list fingerprints); this
// adds the near-duplicate allow-list entry and reloads the index right after each change, so
// the next copy is caught, or let through, at once.
type CorpusLearning struct {
	Pool    *pgxpool.Pool
	Clock   clock.Clock
	Hasher  blacklist.Hasher
	Key     []byte             // the deployment's number-hash key
	Index   *Corpus            // reloaded after a change; nil: it picks the change up on its next refresh
	Persons reviewpg.PersonMap // records the deciding admin on entries; nil: not recorded
	Log     zerolog.Logger
}

var _ review.Corpus = (*CorpusLearning)(nil)

func (c *CorpusLearning) store() reviewpg.Corpus {
	return reviewpg.Corpus{Pool: c.Pool, HashKey: c.Key, Persons: c.Persons, Clock: c.Clock}
}

// Confirm implements review.Corpus.
func (c *CorpusLearning) Confirm(ctx context.Context, conf review.Confirmation) (int64, error) {
	id, err := c.store().Confirm(ctx, conf)
	if err != nil {
		return 0, err
	}
	c.reload(ctx)
	return id, nil
}

// EntryFor implements review.Corpus.
func (c *CorpusLearning) EntryFor(ctx context.Context, group wa.JID, msg wa.MessageID) (int64, bool, error) {
	return c.store().EntryFor(ctx, group, msg)
}

// Retract implements review.Corpus: the entry stops matching at once.
func (c *CorpusLearning) Retract(ctx context.Context, entryID int64) error {
	if err := c.store().Retract(ctx, entryID); err != nil {
		return err
	}
	c.reload(ctx)
	return nil
}

// AllowFingerprint implements review.Corpus: the overturned message's fingerprints, never its
// text, join the group's allow-lists, so neither it nor a near-copy matches again, whatever entry
// it would have matched (DESIGN.md §7.5 "Overturns remove what was learned"): the exact SimHash
// and media hashes (rules fingerprints) and the near-duplicate fingerprint (corpus_allow).
func (c *CorpusLearning) AllowFingerprint(ctx context.Context, f review.FingerprintAllow) error {
	exactErr := c.store().AllowFingerprint(ctx, f)
	var text string
	if f.EntryID != 0 {
		e, err := store.GetCorpusEntry(ctx, c.Pool, f.EntryID)
		if err != nil {
			return errors.Join(exactErr, fmt.Errorf("allow fingerprint of entry %d: %w", f.EntryID, err))
		}
		text = e.TextNorm
	}
	if text == "" && f.Text != "" {
		env := &envelope.Envelope{Group: f.Group, MsgID: f.Message.ID, Texts: []envelope.TextSurface{{Source: "body", Raw: f.Text}}}
		pipeline.NormalizeTexts(env)
		text = CorpusText(c.Hasher, c.Key)(env)
	}
	fp := neardup.Compute(neardup.Key(text))
	if fp.Empty() {
		c.reload(ctx)
		return exactErr
	}
	if err := db.New(c.Pool).InsertCorpusAllow(ctx, db.InsertCorpusAllowParams{
		GroupJid: string(f.Group), Simhash: int64(fp.SimHash), Minhash: fp.MinHashBytes(), Tokens: int32(fp.Tokens),
	}); err != nil {
		return errors.Join(exactErr, fmt.Errorf("allow fingerprint in %s: %w", f.Group, err))
	}
	c.reload(ctx)
	return exactErr
}

// CorpusText returns the whole text of a message the way corpus.FromMessage stores an entry's:
// each surface redacted (the spam's own numbers become keyed-hash tokens, third-party numbers,
// emails and mentions are masked) and normalized. The envelope must have been normalized
// (pipeline.NormalizeTexts), as the fast lane does before its analyzers run.
func CorpusText(h blacklist.Hasher, key []byte) func(e *envelope.Envelope) string {
	return func(e *envelope.Envelope) string {
		return corpus.FromMessage(e, corpus.Options{Hasher: h, HashKey: key}).TextNorm
	}
}

func (c *CorpusLearning) reload(ctx context.Context) {
	if c.Index == nil {
		return
	}
	if err := c.Index.Reload(ctx); err != nil {
		c.Log.Warn().Err(err).Msg("corpus reload after a change; the periodic refresh will retry")
	}
}
