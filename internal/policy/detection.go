package policy

import (
	"context"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// DetectionRecord is one row of the detections table (DESIGN.md §14): what was found, which route
// was taken, and what was done or, in shadow mode, would have been done. It holds no message
// text.
type DetectionRecord struct {
	ID          int64
	Group       wa.JID
	MsgID       wa.MessageID
	EditSeq     int // 0 for the original message, n for its n-th edit
	PersonID    *int64
	Score       float64
	TierReached string
	Signals     []Signal
	Violations  []Violation
	Verdict     Verdict
	Source      Source
	Route       Route
	RuleVersion string
	ActionPlan  []Action
	Mode        DetectionMode
	EvidenceRef string
	Reason      string
	CreatedAt   time.Time
}

// DetectionRef identifies the message and evidence a detection is about.
type DetectionRef struct {
	Group       wa.JID
	MsgID       wa.MessageID
	EditSeq     int // 0 for the original message, n for its n-th edit
	PersonID    *int64
	TierReached string // the highest tier that ran: t0..t4
	RuleVersion string // the weights/rules version that produced the finding
	EvidenceRef string
}

// NewDetection builds the detection row for a planned decision. It returns false when the
// plan says nothing is recorded (clean messages, paused groups).
func NewDetection(ref DetectionRef, f Finding, d Decision, p Plan) (DetectionRecord, bool) {
	if !p.Record {
		return DetectionRecord{}, false
	}
	return DetectionRecord{
		Group: ref.Group, MsgID: ref.MsgID, EditSeq: ref.EditSeq, PersonID: ref.PersonID,
		Score: f.Score, TierReached: ref.TierReached,
		Signals: f.Signals, Violations: f.Violations,
		Verdict: d.Verdict, Source: f.Source, Route: d.Route,
		RuleVersion: ref.RuleVersion, ActionPlan: p.Would, Mode: p.Mode,
		EvidenceRef: ref.EvidenceRef, Reason: d.Reason,
	}, true
}

// DetectionStore keeps detections. Implementations live in the store package.
type DetectionStore interface {
	// RecordDetection stores d and reports whether it was new. The first decision for a
	// message version (group, message, edit) in a mode is kept: a redelivery or a later
	// decision with the same key gets the stored record back with created=false, and the
	// caller must not enqueue its actions again.
	RecordDetection(ctx context.Context, d DetectionRecord) (stored DetectionRecord, created bool, err error)
}

// Record is NewDetection for an Engine outcome.
func (o Outcome) Record(ref DetectionRef) (DetectionRecord, bool) {
	return NewDetection(ref, o.Finding, o.Decision, o.Plan)
}
