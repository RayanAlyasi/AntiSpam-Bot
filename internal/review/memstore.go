package review

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

type postedKey struct {
	chat wa.JID
	id   wa.MessageID
}

// memStore is the in-memory Store behind MemoryStore.
type memStore struct {
	mu     sync.Mutex
	items  map[int64]Item
	codes  map[string]int64
	votes  map[int64][]VoteRecord
	posted map[postedKey]Posted
	nextID int64
}

func newMemStore() *memStore {
	return &memStore{
		items:  map[int64]Item{},
		codes:  map[string]int64{},
		votes:  map[int64][]VoteRecord{},
		posted: map[postedKey]Posted{},
	}
}

func (m *memStore) Create(ctx context.Context, it Item) (Item, error) {
	if err := ctx.Err(); err != nil {
		return Item{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, taken := m.codes[it.Code]; taken {
		return Item{}, ErrCodeTaken
	}
	m.nextID++
	it.ID = m.nextID
	m.items[it.ID] = it
	m.codes[it.Code] = it.ID
	return it, nil
}

func (m *memStore) Get(ctx context.Context, id int64) (Item, error) {
	if err := ctx.Err(); err != nil {
		return Item{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[id]
	if !ok {
		return Item{}, ErrNotFound
	}
	return it, nil
}

func (m *memStore) ByCode(ctx context.Context, code string) (Item, error) {
	m.mu.Lock()
	id, ok := m.codes[code]
	m.mu.Unlock()
	if !ok {
		return Item{}, ErrNotFound
	}
	return m.Get(ctx, id)
}

func (m *memStore) Decide(ctx context.Context, id int64, d Decided) (Item, bool, error) {
	if err := ctx.Err(); err != nil {
		return Item{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[id]
	if !ok {
		return Item{}, false, ErrNotFound
	}
	if it.Status != StatusPending {
		return it, false, nil
	}
	it.Status, it.Decision, it.DecidedBy, it.DecidedByName, it.Method, it.DecidedAt = d.Status, d.Decision, d.By, d.ByName, d.Method, d.At
	if d.Decision == DecisionNotSpam {
		// No text of an overturned message is kept (§7.5).
		it.Evidence.Text, it.Evidence.Thumbnail = "", nil
	}
	m.items[id] = it
	return it, true, nil
}

func (m *memStore) AddVote(ctx context.Context, v VoteRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[v.ReviewID]; !ok {
		return ErrNotFound
	}
	m.votes[v.ReviewID] = append(m.votes[v.ReviewID], v)
	return nil
}

func (m *memStore) Votes(ctx context.Context, id int64) ([]VoteRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.votes[id]), nil
}

func (m *memStore) AddPosted(ctx context.Context, p Posted) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[p.ReviewID]; !ok {
		return ErrNotFound
	}
	m.posted[postedKey{p.Chat, p.ID}] = p
	return nil
}

func (m *memStore) PostedFor(ctx context.Context, id int64) ([]Posted, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Posted
	for _, p := range m.posted {
		if p.ReviewID == id {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b Posted) int { return a.SentAt.Compare(b.SentAt) })
	return out, nil
}

func (m *memStore) ByMessage(ctx context.Context, chat wa.JID, id wa.MessageID) (Item, error) {
	m.mu.Lock()
	p, ok := m.posted[postedKey{chat, id}]
	m.mu.Unlock()
	if !ok {
		return Item{}, ErrNotFound
	}
	return m.Get(ctx, p.ReviewID)
}

func (m *memStore) MarkOutcomeEdited(ctx context.Context, chat wa.JID, id wa.MessageID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := postedKey{chat, id}
	p, ok := m.posted[k]
	if !ok {
		return ErrNotFound
	}
	p.OutcomeEdited = true
	m.posted[k] = p
	return nil
}

func (m *memStore) list(ctx context.Context, keep func(Item) bool) ([]Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Item
	for _, it := range m.items {
		if it.Status == StatusPending && keep(it) {
			out = append(out, it)
		}
	}
	slices.SortFunc(out, func(a, b Item) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return int(a.ID - b.ID)
	})
	return out, nil
}

func (m *memStore) Pending(ctx context.Context, group wa.JID) ([]Item, error) {
	return m.list(ctx, func(it Item) bool { return group == "" || it.Group == group })
}

func (m *memStore) Due(ctx context.Context, now time.Time) ([]Item, error) {
	return m.list(ctx, func(it Item) bool { return !it.Deadline.After(now) })
}

type evidenceKey struct {
	group wa.JID
	msg   wa.MessageID
}

// memEvidence is the in-memory EvidenceStore behind MemoryEvidence.
type memEvidence struct {
	mu    sync.Mutex
	snaps map[evidenceKey]Snapshot
}

func newMemEvidence() *memEvidence { return &memEvidence{snaps: map[evidenceKey]Snapshot{}} }

func (m *memEvidence) Save(ctx context.Context, s Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snaps[evidenceKey{s.Group, s.Message.ID}] = s
	return nil
}

func (m *memEvidence) Get(ctx context.Context, group wa.JID, msg wa.MessageID) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.snaps[evidenceKey{group, msg}]
	if !ok {
		return Snapshot{}, ErrNotFound
	}
	return s, nil
}

func (m *memEvidence) DeleteText(ctx context.Context, group wa.JID, msg wa.MessageID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := evidenceKey{group, msg}
	s, ok := m.snaps[k]
	if !ok {
		return ErrNotFound
	}
	s.Text, s.Thumbnail = "", nil
	m.snaps[k] = s
	return nil
}

func (m *memEvidence) Purge(ctx context.Context, now time.Time) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k, s := range m.snaps {
		if !s.ExpiresAt.IsZero() && !s.ExpiresAt.After(now) {
			delete(m.snaps, k)
			n++
		}
	}
	return n, nil
}
