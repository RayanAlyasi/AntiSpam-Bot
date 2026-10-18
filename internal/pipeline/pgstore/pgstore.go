// Package pgstore is the fast lane's Postgres store: the detection row, the local ban, the
// enqueued actions and the review items of one message are written in one transaction
// (DESIGN.md §4.1, F-088), through store.PolicyStore, the action queue and the review store
// bound to that transaction.
package pgstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	reviewpg "github.com/RayanAlyasi/AntiSpam-Bot/internal/review/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// EnqueueFunc enqueues one action inside the caller's transaction, such as
// (*actions.PGStore).EnqueueTx.
type EnqueueFunc func(ctx context.Context, tx db.DBTX, a actions.Action) error

// Store implements pipeline.Store on Postgres.
type Store struct {
	db      store.TxBeginner
	enqueue EnqueueFunc
	reviews pipeline.Reviews
}

// New returns a store that starts transactions on conn (usually the pool), enqueues actions
// with enqueue and opens reviews with reviews. The context passed to reviews carries the
// transaction (ContextWithTx): a review service whose store and queue write through Conn
// commits its items with the detection.
func New(conn store.TxBeginner, enqueue EnqueueFunc, reviews pipeline.Reviews) *Store {
	return &Store{db: conn, enqueue: enqueue, reviews: reviews}
}

var _ pipeline.Store = (*Store)(nil)

// Processed implements pipeline.Store: a message with a detection was handled. Clean messages
// leave no detection and are simply evaluated again, which writes nothing.
func (s *Store) Processed(ctx context.Context, group wa.JID, msg wa.MessageID, _ string) (bool, error) {
	q, ok := s.db.(db.DBTX)
	if !ok {
		return false, nil
	}
	found, err := db.New(q).DetectionExists(ctx, db.DetectionExistsParams{GroupJid: string(group), MsgID: string(msg)})
	if err != nil {
		return false, fmt.Errorf("look up detection %s/%s: %w", group, msg, err)
	}
	return found, nil
}

// InTx implements pipeline.Store.
func (s *Store) InTx(ctx context.Context, fn func(ctx context.Context, tx pipeline.Tx) error) error {
	return store.InTx(ctx, s.db, func(tx pgx.Tx) error {
		return fn(ContextWithTx(ctx, tx), &txn{tx: tx, q: db.New(tx), policy: store.NewPolicyStore(nil).WithTx(tx), enqueue: s.enqueue, reviews: s.reviews})
	})
}

type txn struct {
	tx      pgx.Tx
	q       *db.Queries
	policy  *store.PolicyStore
	enqueue EnqueueFunc
	reviews pipeline.Reviews
}

// Claim locks the message for the rest of the transaction and reports false when it already has
// a detection. The inbox (F-014) dedupes redelivered events by chat, ID and edit before they get
// here; this second guard keeps a replayed or concurrently claimed message from being recorded
// and acted on twice. Messages that never produce a detection (clean ones) are simply
// re-evaluated, which writes nothing.
func (t *txn) Claim(ctx context.Context, group wa.JID, msg wa.MessageID, _ string) (bool, error) {
	if err := t.q.LockMessage(ctx, string(group)+"/"+string(msg)); err != nil {
		return false, fmt.Errorf("lock message %s/%s: %w", group, msg, err)
	}
	found, err := t.q.DetectionExists(ctx, db.DetectionExistsParams{GroupJid: string(group), MsgID: string(msg)})
	if err != nil {
		return false, fmt.Errorf("look up detection %s/%s: %w", group, msg, err)
	}
	return !found, nil
}

// InsertDetection stores a detection row through store.PolicyStore in this transaction.
func (t *txn) InsertDetection(ctx context.Context, d policy.DetectionRecord) (int64, error) {
	rec, _, err := t.policy.RecordDetection(ctx, d)
	if err != nil {
		return 0, err
	}
	return rec.ID, nil
}

// CreateBan records an active ban through store.PolicyStore in this transaction, or returns the
// person's existing active one in the group.
func (t *txn) CreateBan(ctx context.Context, b policy.GroupBan) (policy.GroupBan, bool, error) {
	return t.policy.CreateBan(ctx, b)
}

// Enqueue implements pipeline.Tx.
func (t *txn) Enqueue(ctx context.Context, a actions.Action) error {
	if t.enqueue == nil {
		return errors.New("enqueue: no action queue configured")
	}
	return t.enqueue(ctx, t.tx, a)
}

// OpenReview implements pipeline.Tx.
func (t *txn) OpenReview(ctx context.Context, n review.NewItem) error {
	if t.reviews == nil {
		return errors.New("open review: no review service configured")
	}
	_, err := t.reviews.Open(ContextWithTx(ctx, t.tx), n)
	return err
}

// SaveEvidence implements pipeline.EvidenceTx: the snapshot commits with the detection.
func (t *txn) SaveEvidence(ctx context.Context, s review.Snapshot) error {
	return reviewpg.Evidence{DB: t.tx}.Save(ctx, s)
}

// PlannedAction is how an action is stored in detections.action_plan (the store.PolicyStore
// encoding).
type PlannedAction struct {
	Kind     string         `json:"kind"`
	Priority int            `json:"priority"`
	Review   *PlannedReview `json:"review,omitempty"`
}

// PlannedReview is a planned review in detections.action_plan.
type PlannedReview struct {
	Kind       string `json:"kind"`
	ProposeBan bool   `json:"propose_ban"`
	Timeout    string `json:"timeout"`
	OnTimeout  string `json:"on_timeout"`
	Batch      string `json:"batch,omitempty"`
}

// PlanJSON encodes an action plan for detections.action_plan.
func PlanJSON(acts []policy.Action) []PlannedAction {
	out := make([]PlannedAction, 0, len(acts))
	for _, a := range acts {
		p := PlannedAction{Kind: string(a.Kind), Priority: a.Priority}
		if r := a.Review; r != nil {
			p.Review = &PlannedReview{
				Kind: string(r.Kind), ProposeBan: r.ProposeBan, Timeout: r.Timeout.String(),
				OnTimeout: string(r.OnTimeout), Batch: r.Batch,
			}
		}
		out = append(out, p)
	}
	return out
}
