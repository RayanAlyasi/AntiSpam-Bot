package fastlane

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/corpus"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/neardup"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// CorpusLearning is review.Corpus on Postgres (F-113/F-114, DESIGN.md §20 v0.1 "where known spam
// comes from"): a message an admin confirms as spam becomes a group-scoped, active corpus entry
// (redacted text, fingerprints, hashed numbers), and an overturn retracts it. The near-duplicate
// index reloads right after each change, so the next copy is caught at once.
type CorpusLearning struct {
	Pool   *pgxpool.Pool
	Clock  clock.Clock
	Hasher blacklist.Hasher
	Key    []byte  // the deployment's number-hash key
	Index  *Corpus // reloaded after a change; nil: it picks the change up on its next refresh
	Log    zerolog.Logger
}

var _ review.Corpus = (*CorpusLearning)(nil)

// Confirm implements review.Corpus.
func (c *CorpusLearning) Confirm(ctx context.Context, conf review.Confirmation) (int64, error) {
	e := &envelope.Envelope{Group: conf.Group, MsgID: conf.Message.ID, Sender: envelope.Identity{LID: conf.Message.Sender}}
	if conf.Text != "" {
		e.Texts = []envelope.TextSurface{{Source: "body", Raw: conf.Text}}
	}
	pipeline.NormalizeTexts(e)
	src := corpus.SourceReview
	if conf.Source == "command" {
		src = corpus.SourceCommand
	}
	at := conf.At
	if at.IsZero() {
		at = c.Clock.Now()
	}
	ent := corpus.FromMessage(e, corpus.Options{
		Scope: corpus.ScopeGroup, GroupJID: string(conf.Group), Status: corpus.StatusActive, Source: src,
		Hasher: c.Hasher, HashKey: c.Key, Confidence: 1, Now: at,
	})
	if conf.ReviewID != 0 {
		id := conf.ReviewID
		ent.SourceReviewID = &id
	}
	var id int64
	err := store.InTx(ctx, c.Pool, func(tx pgx.Tx) error {
		var err error
		if id, err = store.InsertCorpusEntry(ctx, tx, ent); err != nil {
			return err
		}
		return db.New(tx).PutCorpusMessage(ctx, db.PutCorpusMessageParams{GroupJid: string(conf.Group), MsgID: string(conf.Message.ID), EntryID: id})
	})
	if err != nil {
		return 0, fmt.Errorf("confirm spam %s/%s: %w", conf.Group, conf.Message.ID, err)
	}
	c.reload(ctx)
	return id, nil
}

// EntryFor implements review.Corpus.
func (c *CorpusLearning) EntryFor(ctx context.Context, group wa.JID, msg wa.MessageID) (int64, bool, error) {
	id, err := db.New(c.Pool).GetCorpusMessage(ctx, db.GetCorpusMessageParams{GroupJid: string(group), MsgID: string(msg)})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("corpus entry for %s/%s: %w", group, msg, err)
	}
	return id, true, nil
}

// Retract implements review.Corpus: the entry stops matching at once.
func (c *CorpusLearning) Retract(ctx context.Context, entryID int64) error {
	if _, err := store.RetractCorpusEntry(ctx, c.Pool, entryID); err != nil {
		return fmt.Errorf("retract corpus entry %d: %w", entryID, err)
	}
	c.reload(ctx)
	return nil
}

// AllowFingerprint implements review.Corpus: the overturned message's fingerprint, never its text,
// joins the group's allow-list, so neither it nor a near-copy matches again, whatever entry it
// would have matched (DESIGN.md §7.5 "Overturns remove what was learned").
func (c *CorpusLearning) AllowFingerprint(ctx context.Context, f review.FingerprintAllow) error {
	var text string
	if f.EntryID != 0 {
		e, err := store.GetCorpusEntry(ctx, c.Pool, f.EntryID)
		if err != nil {
			return fmt.Errorf("allow fingerprint of entry %d: %w", f.EntryID, err)
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
		return nil
	}
	if err := db.New(c.Pool).InsertCorpusAllow(ctx, db.InsertCorpusAllowParams{
		GroupJid: string(f.Group), Simhash: int64(fp.SimHash), Minhash: fp.MinHashBytes(), Tokens: int32(fp.Tokens),
	}); err != nil {
		return fmt.Errorf("allow fingerprint in %s: %w", f.Group, err)
	}
	c.reload(ctx)
	return nil
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
