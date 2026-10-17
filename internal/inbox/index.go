package inbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Verdicts recorded in message_index.
const (
	VerdictClean  = "clean"
	VerdictSpam   = "spam"
	VerdictPolicy = "policy"
)

// IndexEntry is one processed group message, without any text (DESIGN.md §14, §17): enough to
// map edits to the original, find a banned person's recent messages for cleanup, apply the
// two-day delete window and run dry-runs.
type IndexEntry struct {
	Group        wa.JID
	MsgID        wa.MessageID
	SenderPerson int64 // persons.id; 0 when unknown
	At           time.Time
	Fingerprint  []byte   // the envelope's canonical fingerprint (no text)
	SimHash      *uint64  // SimHash of the normalized text, if there was text
	URLDomains   []string // registrable domains of the message's links
	MediaSHA256  [][]byte
	Verdict      string // VerdictClean, VerdictSpam or VerdictPolicy
	Actioned     bool   // something was done about it (deleted, sender banned)
}

// Expiry is when an entry is purged: 7 days for normal messages, 30 for spam, policy or
// actioned ones (DESIGN.md §14).
func (e IndexEntry) Expiry() time.Time {
	if e.Verdict != VerdictClean || e.Actioned {
		return e.At.Add(IndexActionRetention)
	}
	return e.At.Add(IndexRetention)
}

// Index writes and reads message_index.
type Index struct{ q *db.Queries }

// NewIndex builds the index store.
func NewIndex(pool *pgxpool.Pool) *Index { return &Index{q: db.New(pool)} }

// Put records or updates an entry. A later write (an edit, a late detection) can raise the
// verdict and extend the expiry, never lower them.
func (x *Index) Put(ctx context.Context, e IndexEntry) error {
	if e.Verdict == "" {
		e.Verdict = VerdictClean
	}
	switch e.Verdict {
	case VerdictClean, VerdictSpam, VerdictPolicy:
	default:
		return fmt.Errorf("message_index %s: unknown verdict %q", e.MsgID, e.Verdict)
	}
	if !e.Group.IsGroup() {
		return fmt.Errorf("message_index %s: %q is not a group", e.MsgID, e.Group)
	}
	var sender pgtype.Int8
	if e.SenderPerson != 0 {
		sender = pgtype.Int8{Int64: e.SenderPerson, Valid: true}
	}
	var sim pgtype.Int8
	if e.SimHash != nil {
		sim = pgtype.Int8{Int64: int64(*e.SimHash), Valid: true} // bit pattern kept; Postgres has no uint64
	}
	domains := e.URLDomains
	if domains == nil {
		domains = []string{}
	}
	media := e.MediaSHA256
	if media == nil {
		media = [][]byte{}
	}
	err := x.q.UpsertMessageIndex(ctx, db.UpsertMessageIndexParams{
		GroupJid: string(e.Group), MsgID: string(e.MsgID), SenderPerson: sender, Ts: e.At,
		Fingerprint: e.Fingerprint, Simhash: sim, UrlDomains: domains, MediaSha256: media,
		Verdict: e.Verdict, Actioned: e.Actioned, ExpiresAt: e.Expiry(),
	})
	if err != nil {
		return fmt.Errorf("upsert message_index %s: %w", e.MsgID, err)
	}
	return nil
}

// Get reads one entry.
func (x *Index) Get(ctx context.Context, group wa.JID, id wa.MessageID) (IndexEntry, bool, error) {
	r, err := x.q.GetMessageIndex(ctx, db.GetMessageIndexParams{GroupJid: string(group), MsgID: string(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return IndexEntry{}, false, nil
	}
	if err != nil {
		return IndexEntry{}, false, fmt.Errorf("get message_index %s: %w", id, err)
	}
	e := IndexEntry{
		Group: wa.JID(r.GroupJid), MsgID: wa.MessageID(r.MsgID), SenderPerson: r.SenderPerson.Int64, At: r.Ts,
		Fingerprint: r.Fingerprint, URLDomains: r.UrlDomains, MediaSHA256: r.MediaSha256, Verdict: r.Verdict, Actioned: r.Actioned,
	}
	if r.Simhash.Valid {
		v := uint64(r.Simhash.Int64)
		e.SimHash = &v
	}
	return e, true, nil
}
