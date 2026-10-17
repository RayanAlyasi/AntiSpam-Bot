package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/corpus"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/fingerprint"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
)

// ErrInvalidCorpusEntry is returned for an entry that can't be stored.
var ErrInvalidCorpusEntry = errors.New("invalid corpus entry")

// InsertCorpusEntry stores a confirmed-spam entry built by corpus.FromMessage and returns its
// ID. The label is always spam; the table rejects anything else (§7.5).
func InsertCorpusEntry(ctx context.Context, q db.DBTX, e corpus.Entry) (int64, error) {
	if e.Scope == corpus.ScopeGroup && e.GroupJID == "" {
		return 0, fmt.Errorf("%w: group entry without a group", ErrInvalidCorpusEntry)
	}
	if e.ExpiresAt.IsZero() {
		return 0, fmt.Errorf("%w: no expiry", ErrInvalidCorpusEntry)
	}
	var sim pgtype.Int8
	if e.SimHash != nil {
		sim = pgtype.Int8{Int64: e.SimHash.Int64(), Valid: true}
	}
	id, err := db.New(q).InsertCorpusEntry(ctx, db.InsertCorpusEntryParams{
		Kind: string(e.Kind), Scope: string(e.Scope), GroupJid: optText(e.GroupJID), Status: string(e.Status),
		Source: string(e.Source), ImportBatchID: optInt8(e.ImportBatchID), SourceReviewID: optInt8(e.SourceReviewID),
		SourceActionID: optInt8(e.SourceActionID), DecidedByPerson: optInt8(e.DecidedByPerson),
		TextRawRedacted: e.TextRawRedacted, TextNorm: e.TextNorm, Simhash: sim, Minhash: e.MinHash,
		Keywords: orEmpty(e.Keywords), Urls: orEmpty(e.URLs), Domains: orEmpty(e.Domains),
		NumberHashes: orEmpty(e.NumberHashes), ChannelJids: orEmpty(e.ChannelJIDs), InviteCodes: orEmpty(e.InviteCodes),
		MediaSha256: orEmpty(e.MediaSHA256), Phashes: orEmpty(e.PHashes), Confidence: e.Confidence,
		CreatedAt: e.CreatedAt, ExpiresAt: e.ExpiresAt,
	})
	if err != nil {
		return 0, fmt.Errorf("insert corpus entry: %w", err)
	}
	return id, nil
}

// GetCorpusEntry reads one entry.
func GetCorpusEntry(ctx context.Context, q db.DBTX, id int64) (corpus.Entry, error) {
	r, err := db.New(q).GetCorpusEntry(ctx, id)
	if err != nil {
		return corpus.Entry{}, fmt.Errorf("get corpus entry %d: %w", id, err)
	}
	e := corpus.Entry{
		ID: r.ID, Kind: corpus.Kind(r.Kind), Scope: corpus.Scope(r.Scope), GroupJID: r.GroupJid.String,
		Status: corpus.Status(r.Status), Source: corpus.Source(r.Source), ImportBatchID: ptr8(r.ImportBatchID),
		SourceReviewID: ptr8(r.SourceReviewID), SourceActionID: ptr8(r.SourceActionID),
		DecidedByPerson: ptr8(r.DecidedByPerson), TextRawRedacted: r.TextRawRedacted, TextNorm: r.TextNorm,
		MinHash: r.Minhash, Keywords: r.Keywords, URLs: r.Urls, Domains: r.Domains, NumberHashes: r.NumberHashes,
		ChannelJIDs: r.ChannelJids, InviteCodes: r.InviteCodes, MediaSHA256: r.MediaSha256, PHashes: r.Phashes,
		Confidence: r.Confidence, Hits: int(r.Hits), CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt,
	}
	if r.Simhash.Valid {
		h := fingerprint.SimHashFromInt64(r.Simhash.Int64)
		e.SimHash = &h
	}
	if r.LastHitAt.Valid {
		t := r.LastHitAt.Time
		e.LastHitAt = &t
	}
	return e, nil
}

// CorpusFingerprint is what the near-duplicate indexes load for one live entry: no text.
type CorpusFingerprint struct {
	ID       int64
	Scope    corpus.Scope
	GroupJID string
	Status   corpus.Status
	SimHash  *fingerprint.SimHash
	MinHash  *fingerprint.MinHash
}

// ListLiveCorpusFingerprints returns the fingerprints of every provisional, active or promoted
// entry that hasn't expired at now.
func ListLiveCorpusFingerprints(ctx context.Context, q db.DBTX, now time.Time) ([]CorpusFingerprint, error) {
	rows, err := db.New(q).ListLiveCorpusFingerprints(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("list corpus fingerprints: %w", err)
	}
	out := make([]CorpusFingerprint, 0, len(rows))
	for _, r := range rows {
		f := CorpusFingerprint{ID: r.ID, Scope: corpus.Scope(r.Scope), GroupJID: r.GroupJid.String, Status: corpus.Status(r.Status)}
		if r.Simhash.Valid {
			h := fingerprint.SimHashFromInt64(r.Simhash.Int64)
			f.SimHash = &h
		}
		if r.Minhash != nil {
			m, err := fingerprint.ParseMinHash(r.Minhash)
			if err != nil {
				return nil, fmt.Errorf("corpus entry %d: %w", r.ID, err)
			}
			f.MinHash = &m
		}
		out = append(out, f)
	}
	return out, nil
}

// SetCorpusStatus changes an entry's status (provisional → active, promoted, expired); it
// reports false for an unknown entry. Use RetractCorpusEntry for overturns.
func SetCorpusStatus(ctx context.Context, q db.DBTX, id int64, s corpus.Status) (bool, error) {
	n, err := db.New(q).SetCorpusStatus(ctx, db.SetCorpusStatusParams{ID: id, Status: string(s)})
	if err != nil {
		return false, fmt.Errorf("set corpus entry %d status: %w", id, err)
	}
	return n > 0, nil
}

// RecordCorpusHit counts a match against an entry, which keeps its fingerprints from expiring.
func RecordCorpusHit(ctx context.Context, q db.DBTX, id int64, now time.Time) error {
	if err := db.New(q).RecordCorpusHit(ctx, db.RecordCorpusHitParams{ID: id, Now: pgtype.Timestamptz{Time: now, Valid: true}}); err != nil {
		return fmt.Errorf("record corpus hit %d: %w", id, err)
	}
	return nil
}

// ConfirmPattern records that a pattern was confirmed as spam in a group (§7.5); it reports
// false when that group had already confirmed it.
func ConfirmPattern(ctx context.Context, q db.DBTX, patternKey, groupJID string, admin *int64, entryID int64, at time.Time) (bool, error) {
	n, err := db.New(q).ConfirmPattern(ctx, db.ConfirmPatternParams{
		PatternKey: patternKey, GroupJid: groupJID, AdminPersonID: optInt8(admin), CorpusEntryID: entryID, At: at,
	})
	if err != nil {
		return false, fmt.Errorf("confirm pattern: %w", err)
	}
	return n > 0, nil
}

// PatternConfirmation is one corpus_confirmations row.
type PatternConfirmation struct {
	PatternKey    string
	GroupJID      string
	AdminPersonID *int64
	EntryID       int64
	At            time.Time
}

// ListPatternConfirmations returns the groups that confirmed a pattern since a time, oldest
// first: the input of the promotion rule (≥ 3 unrelated groups within 30 days).
func ListPatternConfirmations(ctx context.Context, q db.DBTX, patternKey string, since time.Time) ([]PatternConfirmation, error) {
	rows, err := db.New(q).ListPatternConfirmations(ctx, db.ListPatternConfirmationsParams{PatternKey: patternKey, Since: since})
	if err != nil {
		return nil, fmt.Errorf("list pattern confirmations: %w", err)
	}
	out := make([]PatternConfirmation, len(rows))
	for i, r := range rows {
		out[i] = PatternConfirmation{PatternKey: r.PatternKey, GroupJID: r.GroupJid, AdminPersonID: ptr8(r.AdminPersonID), EntryID: r.CorpusEntryID, At: r.At}
	}
	return out, nil
}

// LinkLearnedRule records that a rule was learned from an entry, so retracting the entry
// removes the rule.
func LinkLearnedRule(ctx context.Context, q db.DBTX, ruleID, entryID int64, groupJID string, at time.Time) error {
	err := db.New(q).LinkLearnedRule(ctx, db.LinkLearnedRuleParams{RuleID: ruleID, CorpusEntryID: entryID, GroupJid: groupJID, CreatedAt: at})
	if err != nil {
		return fmt.Errorf("link learned rule %d: %w", ruleID, err)
	}
	return nil
}

// ListLearnedRules returns the IDs of the rules learned from an entry.
func ListLearnedRules(ctx context.Context, q db.DBTX, entryID int64) ([]int64, error) {
	ids, err := db.New(q).ListLearnedRules(ctx, entryID)
	if err != nil {
		return nil, fmt.Errorf("list learned rules: %w", err)
	}
	return ids, nil
}

// RetractCorpusEntry undoes an entry after a "not spam" overturn (§7.5): its learned rules are
// deleted (which reloads every node's rules), its text and keywords are erased and its status
// becomes retracted. It reports false for an unknown entry.
func RetractCorpusEntry(ctx context.Context, q db.DBTX, id int64) (bool, error) {
	n, err := db.New(q).RetractCorpusEntry(ctx, id)
	if err != nil {
		return false, fmt.Errorf("retract corpus entry %d: %w", id, err)
	}
	return n > 0, nil
}
