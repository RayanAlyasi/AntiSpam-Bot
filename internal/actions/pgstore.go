package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// PGStore is the Store on Postgres: the app.actions and app.action_controls tables.
type PGStore struct {
	pool  *pgxpool.Pool
	clock clock.Clock

	mu    sync.Mutex
	locks map[wa.JID]*lockConn // executor locks held by this store
}

var _ Store = (*PGStore)(nil)

// NewPGStore returns a store on pool that stamps rows with c (clock.Real when nil).
func NewPGStore(pool *pgxpool.Pool, c clock.Clock) *PGStore {
	if c == nil {
		c = clock.Real{}
	}
	return &PGStore{pool: pool, clock: c}
}

// Enqueue implements Store in its own transaction.
func (s *PGStore) Enqueue(ctx context.Context, a Action) (EnqueueResult, error) {
	return s.EnqueueTx(ctx, s.pool, a)
}

// EnqueueTx enqueues a in the caller's transaction (or any db.DBTX), so an action commits or
// rolls back together with the detection that caused it (DESIGN.md §10.1).
func (s *PGStore) EnqueueTx(ctx context.Context, tx db.DBTX, a Action) (EnqueueResult, error) {
	now := s.clock.Now()
	a, dropped, err := prepare(a, now)
	if err != nil {
		return EnqueueResult{}, err
	}
	q := db.New(tx)
	if dropped != "" {
		id, err := q.ActionIDByDedupeKey(ctx, a.DedupeKey)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return EnqueueResult{Dropped: dropped}, nil
		case err != nil:
			return EnqueueResult{}, fmt.Errorf("look up action %q: %w", a.DedupeKey, err)
		}
		return EnqueueResult{ID: id}, nil
	}
	payload, err := json.Marshal(a.Payload)
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("encode %s payload: %w", a.Kind, err)
	}
	id, err := q.InsertAction(ctx, db.InsertActionParams{
		Bot: string(a.Bot), Chat: string(a.Chat), Kind: string(a.Kind), Priority: int16(a.Priority),
		Payload: payload, NotBefore: a.NotBefore, Deadline: a.Deadline, MessageTime: optTime(a.MessageTime),
		DedupeKey: a.DedupeKey, ReasonDetectionID: optInt(a.ReasonDetectionID), CreatedAt: now,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		id, err = q.ActionIDByDedupeKey(ctx, a.DedupeKey)
		if err != nil {
			return EnqueueResult{}, fmt.Errorf("look up duplicate action %q: %w", a.DedupeKey, err)
		}
		return EnqueueResult{ID: id}, nil
	}
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("insert %s action: %w", a.Kind, err)
	}
	return EnqueueResult{ID: id, Inserted: true}, nil
}

// Get implements Store.
func (s *PGStore) Get(ctx context.Context, id int64) (Action, error) {
	row, err := db.New(s.pool).GetAction(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Action{}, fmt.Errorf("action %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return Action{}, fmt.Errorf("get action %d: %w", id, err)
	}
	return fromRow(row)
}

// Cancel implements Store.
func (s *PGStore) Cancel(ctx context.Context, dedupeKey, reason string) (bool, error) {
	n, err := db.New(s.pool).CancelAction(ctx, db.CancelActionParams{DedupeKey: dedupeKey, Reason: reason, Now: s.clock.Now()})
	if err != nil {
		return false, fmt.Errorf("cancel action %q: %w", dedupeKey, err)
	}
	return n > 0, nil
}

// Lock implements Store with a session advisory lock held on a dedicated connection (outside
// the pool, so bots don't use up pool connections) until unlock. One executor per bot holds
// across processes, and a crashed process releases the lock with its connection. HoldsLock
// notices when that connection dies.
func (s *PGStore) Lock(ctx context.Context, bot wa.JID) (func(), error) {
	conn, err := pgx.ConnectConfig(ctx, s.pool.Config().ConnConfig.Copy())
	if err != nil {
		return nil, fmt.Errorf("connect for the executor lock: %w", err)
	}
	closeConn := func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockTimeout)
		defer cancel()
		_ = conn.Close(cctx)
	}
	key := lockKey(bot)
	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&ok); err != nil {
		closeConn()
		return nil, fmt.Errorf("executor lock for %s: %w", maskJID(bot), err)
	}
	if !ok {
		closeConn()
		return nil, fmt.Errorf("bot %s: %w", maskJID(bot), ErrExecutorRunning)
	}
	l := &lockConn{conn: conn}
	s.mu.Lock()
	if s.locks == nil {
		s.locks = map[wa.JID]*lockConn{}
	}
	s.locks[bot] = l
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			if s.locks[bot] == l {
				delete(s.locks, bot)
			}
			s.mu.Unlock()
			l.mu.Lock()
			defer l.mu.Unlock()
			// Closing the connection releases the session lock; unlocking first is a courtesy.
			uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockTimeout)
			defer cancel()
			_, _ = conn.Exec(uctx, "SELECT pg_advisory_unlock($1)", key)
			closeConn()
		})
	}, nil
}

// lockTimeout bounds the lock connection's health checks and its release, so a half-open TCP
// connection can't hold up a call or a shutdown for long.
const lockTimeout = 5 * time.Second

// lockConn is the connection that holds one bot's executor lock. pgx connections aren't safe
// for concurrent use, so mu serialises the health check and the release.
type lockConn struct {
	mu   sync.Mutex
	conn *pgx.Conn
}

// HoldsLock implements Store: the lock lives as long as its connection, so a working
// connection means the lock is still held.
func (s *PGStore) HoldsLock(ctx context.Context, bot wa.JID) error {
	s.mu.Lock()
	l := s.locks[bot]
	s.mu.Unlock()
	if l == nil {
		return fmt.Errorf("bot %s: %w", maskJID(bot), ErrLockLost)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	pctx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	if err := l.conn.Ping(pctx); err != nil {
		return fmt.Errorf("bot %s: %w: %w", maskJID(bot), ErrLockLost, err)
	}
	return nil
}

func lockKey(bot wa.JID) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("antispam.actions.executor:" + string(bot)))
	return int64(h.Sum64()) // any 64-bit key will do
}

// Recover implements Store.
func (s *PGStore) Recover(ctx context.Context, bot wa.JID) (int, error) {
	n, err := db.New(s.pool).RecoverActions(ctx, db.RecoverActionsParams{Bot: string(bot), Now: s.clock.Now()})
	if err != nil {
		return 0, fmt.Errorf("recover running actions: %w", err)
	}
	return int(n), nil
}

// Candidates implements Store.
func (s *PGStore) Candidates(ctx context.Context, bot wa.JID, n int) ([]Action, error) {
	rows, err := db.New(s.pool).CandidateActions(ctx, db.CandidateActionsParams{Bot: string(bot), N: int64(n)})
	if err != nil {
		return nil, fmt.Errorf("read candidate actions: %w", err)
	}
	return fromRows(rows)
}

// Pending implements Store.
func (s *PGStore) Pending(ctx context.Context, bot, chat wa.JID, kind Kind) ([]Action, error) {
	rows, err := db.New(s.pool).PendingActionsInChat(ctx, db.PendingActionsInChatParams{Bot: string(bot), Chat: string(chat), Kind: string(kind)})
	if err != nil {
		return nil, fmt.Errorf("read pending %s actions: %w", kind, err)
	}
	return fromRows(rows)
}

// Claim implements Store with FOR UPDATE SKIP LOCKED.
func (s *PGStore) Claim(ctx context.Context, id int64) (Action, bool, error) {
	row, err := db.New(s.pool).ClaimAction(ctx, db.ClaimActionParams{ID: id, Now: s.clock.Now()})
	if errors.Is(err, pgx.ErrNoRows) {
		return Action{}, false, nil
	}
	if err != nil {
		return Action{}, false, fmt.Errorf("claim action %d: %w", id, err)
	}
	a, err := fromRow(row)
	return a, err == nil, err
}

// Finish implements Store.
func (s *PGStore) Finish(ctx context.Context, id int64, status Status, errText string, res Result) error {
	if !status.Final() {
		return fmt.Errorf("finish action %d: %s is not a final status", id, status)
	}
	result, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("encode action %d result: %w", id, err)
	}
	n, err := db.New(s.pool).FinishAction(ctx, db.FinishActionParams{
		ID: id, Status: string(status), LastError: errText, Result: result, Now: s.clock.Now(),
		Called: status == StatusDone || status == StatusFailed,
	})
	if err != nil {
		return fmt.Errorf("finish action %d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("finish action %d: not running", id)
	}
	return nil
}

// Retry implements Store.
func (s *PGStore) Retry(ctx context.Context, id int64, r RetryInfo) error {
	result, err := json.Marshal(r.Result)
	if err != nil {
		return fmt.Errorf("encode action %d result: %w", id, err)
	}
	n, err := db.New(s.pool).RetryAction(ctx, db.RetryActionParams{
		ID: id, NotBefore: r.NotBefore, LastError: r.Error, Result: result, Now: s.clock.Now(),
		Called: r.Called, Attempted: r.Attempted,
	})
	if err != nil {
		return fmt.Errorf("retry action %d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("retry action %d: not running", id)
	}
	return nil
}

// ExpireDue implements Store.
func (s *PGStore) ExpireDue(ctx context.Context, bot wa.JID, now time.Time) ([]Action, error) {
	rows, err := db.New(s.pool).ExpireDueActions(ctx, db.ExpireDueActionsParams{Bot: string(bot), Now: now})
	if err != nil {
		return nil, fmt.Errorf("expire actions: %w", err)
	}
	return fromRows(rows)
}

// FailPending implements Store.
func (s *PGStore) FailPending(ctx context.Context, bot, chat wa.JID, kinds []Kind, reason string) (int, error) {
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = string(k)
	}
	n, err := db.New(s.pool).FailPendingActions(ctx, db.FailPendingActionsParams{
		Bot: string(bot), Chat: string(chat), Kinds: names, Reason: reason, Now: s.clock.Now(),
	})
	if err != nil {
		return 0, fmt.Errorf("fail pending actions: %w", err)
	}
	return int(n), nil
}

// WritesSince implements Store.
func (s *PGStore) WritesSince(ctx context.Context, bot wa.JID, since time.Time) (int, error) {
	n, err := db.New(s.pool).CountWritesSince(ctx, db.CountWritesSinceParams{Bot: string(bot), Since: since})
	if err != nil {
		return 0, fmt.Errorf("count writes: %w", err)
	}
	return int(n), nil
}

// Control implements Store.
func (s *PGStore) Control(ctx context.Context, scope string) (Control, error) {
	raw, err := db.New(s.pool).GetActionControl(ctx, scope)
	if errors.Is(err, pgx.ErrNoRows) {
		return Control{}, nil
	}
	if err != nil {
		return Control{}, fmt.Errorf("read control %s: %w", scope, err)
	}
	var c Control
	if err := json.Unmarshal(raw, &c); err != nil {
		return Control{}, fmt.Errorf("decode control %s: %w", scope, err)
	}
	return c, nil
}

// UpdateControl implements Store: a locked read-modify-write in one transaction.
func (s *PGStore) UpdateControl(ctx context.Context, scope string, change func(*Control)) (Control, error) {
	var c Control
	err := store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		c, err = s.updateControl(ctx, tx, scope, change)
		return err
	})
	return c, err
}

func (s *PGStore) updateControl(ctx context.Context, tx pgx.Tx, scope string, change func(*Control)) (Control, error) {
	q := db.New(tx)
	now := s.clock.Now()
	raw, err := q.LockActionControl(ctx, db.LockActionControlParams{Scope: scope, Now: now})
	if err != nil {
		return Control{}, fmt.Errorf("lock control %s: %w", scope, err)
	}
	var c Control
	if err := json.Unmarshal(raw, &c); err != nil {
		return Control{}, fmt.Errorf("decode control %s: %w", scope, err)
	}
	change(&c)
	state, err := json.Marshal(c)
	if err != nil {
		return Control{}, fmt.Errorf("encode control %s: %w", scope, err)
	}
	if err := q.SetActionControl(ctx, db.SetActionControlParams{Scope: scope, State: state, Now: now}); err != nil {
		return Control{}, fmt.Errorf("save control %s: %w", scope, err)
	}
	return c, nil
}

func fromRows(rows []db.AppAction) ([]Action, error) {
	out := make([]Action, 0, len(rows))
	for _, r := range rows {
		a, err := fromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func fromRow(r db.AppAction) (Action, error) {
	a := Action{
		ID: r.ID, Bot: wa.JID(r.Bot), Chat: wa.JID(r.Chat), Kind: Kind(r.Kind), Priority: Priority(r.Priority),
		DedupeKey: r.DedupeKey, NotBefore: r.NotBefore, Deadline: r.Deadline,
		Status: Status(r.Status), Attempts: int(r.Attempts), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		LastError: r.LastError,
	}
	if r.MessageTime.Valid {
		a.MessageTime = r.MessageTime.Time
	}
	if r.ReasonDetectionID.Valid {
		a.ReasonDetectionID = r.ReasonDetectionID.Int64
	}
	if err := json.Unmarshal(r.Payload, &a.Payload); err != nil {
		return Action{}, fmt.Errorf("decode action %d payload: %w", r.ID, err)
	}
	if err := json.Unmarshal(r.Result, &a.Result); err != nil {
		return Action{}, fmt.Errorf("decode action %d result: %w", r.ID, err)
	}
	return a, nil
}

func optTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()}
}

func optInt(v int64) pgtype.Int8 { return pgtype.Int8{Int64: v, Valid: v != 0} }

// UpdateControlAudited implements AuditedControl: the change and its audit_log entry commit together.
func (s *PGStore) UpdateControlAudited(ctx context.Context, scope string, change func(*Control) AuditEntry) (Control, error) {
	var c Control
	err := store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var entry AuditEntry
		var err error
		if c, err = s.updateControl(ctx, tx, scope, func(c *Control) { entry = change(c) }); err != nil {
			return err
		}
		return writeAudit(ctx, tx, entry)
	})
	return c, err
}

// PGAuditor writes queue audit entries to audit_log.
type PGAuditor struct{ DB db.DBTX }

// Audit implements Auditor.
func (a PGAuditor) Audit(ctx context.Context, e AuditEntry) error { return writeAudit(ctx, a.DB, e) }

func writeAudit(ctx context.Context, q db.DBTX, e AuditEntry) error {
	diff, err := json.Marshal(e.Diff)
	if err != nil {
		return fmt.Errorf("encode audit diff: %w", err)
	}
	if _, err := store.WriteAudit(ctx, q, store.AuditEntry{Actor: e.Actor, Action: e.Action, Diff: diff}); err != nil {
		return fmt.Errorf("write audit %s: %w", e.Action, err)
	}
	return nil
}
