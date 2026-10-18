package pipeline

import (
	"context"
	"slices"
	"sync"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// MemStore is an in-memory Store for tests and fixture replays: transactions are serialized and
// roll back on error, like the Postgres one. It's safe for concurrent use.
type MemStore struct {
	// Reviews opens the review items of committed transactions. Nil: they're only kept in
	// Opened.
	Reviews  Reviews
	Opened   []review.NewItem
	Evidence []review.Snapshot // committed evidence snapshots

	mu         sync.Mutex
	claimed    map[string]bool
	Detections []policy.DetectionRecord
	Bans       []policy.GroupBan
	Actions    []actions.Action
	dedupe     map[string]bool
}

// NewMemStore returns an empty store.
func NewMemStore() *MemStore {
	return &MemStore{claimed: map[string]bool{}, dedupe: map[string]bool{}}
}

// InTx implements Store.
func (m *MemStore) InTx(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx := &memTx{m: m, claimed: map[string]bool{}, dedupe: map[string]bool{}}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	for k := range tx.claimed {
		m.claimed[k] = true
	}
	for k := range tx.dedupe {
		m.dedupe[k] = true
	}
	for _, d := range tx.dets {
		d.ID = int64(len(m.Detections) + 1)
		m.Detections = append(m.Detections, d)
	}
	m.Bans = append(m.Bans, tx.bans...)
	m.Actions = append(m.Actions, tx.acts...)
	m.Evidence = append(m.Evidence, tx.evidence...)
	for _, n := range tx.reviews {
		m.Opened = append(m.Opened, n)
		if m.Reviews != nil {
			if _, err := m.Reviews.Open(ctx, n); err != nil {
				return err
			}
		}
	}
	return nil
}

// Processed implements Store.
func (m *MemStore) Processed(_ context.Context, group wa.JID, msg wa.MessageID, version string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.claimed[claimKey(group, msg, version)], nil
}

func claimKey(group wa.JID, msg wa.MessageID, version string) string {
	return string(group) + "\x00" + string(msg) + "\x00" + version
}

// Snapshot returns copies of what has been committed.
func (m *MemStore) Snapshot() ([]policy.DetectionRecord, []policy.GroupBan, []actions.Action) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.Detections), slices.Clone(m.Bans), slices.Clone(m.Actions)
}

type memTx struct {
	m        *MemStore
	claimed  map[string]bool
	dedupe   map[string]bool
	dets     []policy.DetectionRecord
	bans     []policy.GroupBan
	acts     []actions.Action
	reviews  []review.NewItem
	evidence []review.Snapshot
}

func (t *memTx) Claim(_ context.Context, group wa.JID, msg wa.MessageID, version string) (bool, error) {
	k := claimKey(group, msg, version)
	if t.m.claimed[k] || t.claimed[k] {
		return false, nil
	}
	t.claimed[k] = true
	return true, nil
}

func (t *memTx) InsertDetection(_ context.Context, d policy.DetectionRecord) (int64, error) {
	t.dets = append(t.dets, d)
	return int64(len(t.m.Detections) + len(t.dets)), nil
}

func (t *memTx) CreateBan(_ context.Context, b policy.GroupBan) (policy.GroupBan, bool, error) {
	for _, x := range slices.Concat(t.m.Bans, t.bans) {
		if x.Active && x.Group == b.Group && x.PersonID == b.PersonID {
			return x, false, nil
		}
	}
	b.ID, b.Active = int64(len(t.m.Bans)+len(t.bans)+1), true
	t.bans = append(t.bans, b)
	return b, true, nil
}

func (t *memTx) Enqueue(_ context.Context, a actions.Action) error {
	if t.m.dedupe[a.DedupeKey] || t.dedupe[a.DedupeKey] {
		return nil
	}
	t.dedupe[a.DedupeKey] = true
	t.acts = append(t.acts, a)
	return nil
}

func (t *memTx) OpenReview(_ context.Context, n review.NewItem) error {
	t.reviews = append(t.reviews, n)
	return nil
}

func (t *memTx) SaveEvidence(_ context.Context, s review.Snapshot) error {
	t.evidence = append(t.evidence, s)
	return nil
}
