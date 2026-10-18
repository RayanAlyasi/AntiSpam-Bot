package review

import (
	"context"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// EvidenceRetention is how long evidence snapshots are kept (DESIGN.md §14 retention).
const EvidenceRetention = 30 * 24 * time.Hour

// Snapshot is the evidence kept for a deleted message: redacted text and the inline thumbnail.
type Snapshot struct {
	Group       wa.JID
	Message     wa.MessageRef
	Text        string // redacted on save
	Thumbnail   *wa.Image
	DetectionID int64
	Reason      string
	At          time.Time
	ExpiresAt   time.Time
}

// EvidenceStore keeps evidence snapshots.
type EvidenceStore interface {
	Save(ctx context.Context, s Snapshot) error
	// Get returns ErrNotFound when there is no snapshot.
	Get(ctx context.Context, group wa.JID, msg wa.MessageID) (Snapshot, error)
	// DeleteText removes the snapshot's text (and thumbnail) but keeps the row for the audit trail.
	DeleteText(ctx context.Context, group wa.JID, msg wa.MessageID) error
	// Purge deletes snapshots that expired at or before now.
	Purge(ctx context.Context, now time.Time) (int, error)
}

// Confirmation is spam an admin confirmed (.spam, review ✅, .ban spam): it becomes a corpus entry.
type Confirmation struct {
	Group     wa.JID
	Message   wa.MessageRef // Message.Sender is the spammer
	Text      string        // the raw text; the corpus redacts it on save
	Thumbnail *wa.Image
	// Envelope is the whole message when the caller has it (links, media hashes, contacts);
	// without it the corpus is built from Text alone.
	Envelope *envelope.Envelope
	Admin    wa.JID
	Source   string // review | command
	ReviewID int64
	At       time.Time
}

// FingerprintAllow is an allow-list entry for an overturned message: only its fingerprint is
// kept, never its text (§7.5 Overturns).
type FingerprintAllow struct {
	Group   wa.JID
	Message wa.MessageRef
	Text    string // used to compute the fingerprint, then dropped
	EntryID int64  // the corpus entry being retracted, whose stored fingerprints can be used; 0 if none
}

// Corpus is the confirmed-spam corpus (F-113) as learning needs it.
type Corpus interface {
	Confirm(ctx context.Context, c Confirmation) (entryID int64, err error)
	// EntryFor returns the corpus entry created from a message, if any.
	EntryFor(ctx context.Context, group wa.JID, msg wa.MessageID) (entryID int64, ok bool, err error)
	// Retract removes an entry and the rules it created from the matching indexes at once
	// (NOTIFY) and deletes its text.
	Retract(ctx context.Context, entryID int64) error
	// AllowFingerprint stores the fingerprint of an overturned message (the entry's stored
	// fingerprints when EntryID is set, otherwise one computed from Text). With neither, it must
	// store nothing: a fingerprint of empty text would allow-list every text-less message.
	AllowFingerprint(ctx context.Context, f FingerprintAllow) error
}

// Standing is a member's trust and strikes in a group.
type Standing interface {
	RemoveStrike(ctx context.Context, group, person wa.JID, msg wa.MessageID) error
	RestoreTrust(ctx context.Context, group, person wa.JID) error
	RecordFalsePositive(ctx context.Context, group wa.JID, msg wa.MessageRef, actor wa.JID) error
}

// Overturn is a not-spam decision: .notspam/.ok, review ❌, or an overturned .spam or bot deletion.
type Overturn struct {
	Group   wa.JID
	Message wa.MessageRef // Message.Sender is the member whose trust is restored
	Actor   wa.JID
	Method  Method
}

// LearningDeps are Learning's collaborators.
type LearningDeps struct {
	Evidence EvidenceStore
	Corpus   Corpus
	Standing Standing
	Clock    clock.Clock
}

// Learning keeps evidence and turns admin decisions into corpus entries and retractions
// (§7.5, F-114, F-118).
type Learning struct {
	d LearningDeps
}

var _ Learner = (*Learning)(nil)

// NewLearning returns a Learning.
func NewLearning(d LearningDeps) *Learning {
	if d.Clock == nil {
		d.Clock = clock.Real{}
	}
	return &Learning{d: d}
}

// Snapshot keeps evidence for a deleted message: the text is redacted and the snapshot expires
// after EvidenceRetention.
func (l *Learning) Snapshot(ctx context.Context, s Snapshot) error {
	return l.snapshot(ctx, s)
}

// ConfirmSpam stores spam an admin confirmed as a corpus entry, so exact and near-duplicate
// matching catch it next time.
func (l *Learning) ConfirmSpam(ctx context.Context, c Confirmation) error {
	return l.confirmSpam(ctx, c)
}

// Overturn handles a not-spam decision: the message's fingerprint is allow-listed, any corpus
// entry it produced is retracted, its evidence text is deleted, the strike is removed, trust
// is restored and the false positive is recorded. No text of the message remains afterwards.
func (l *Learning) Overturn(ctx context.Context, o Overturn) error {
	return l.overturn(ctx, o)
}
