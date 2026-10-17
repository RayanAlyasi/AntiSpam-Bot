package actions

import (
	"context"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Store persists actions and the queue's control state. The table is the source of truth: the
// executor keeps only a window of candidates in memory, so a crash loses nothing (DESIGN.md §10.1).
// MemStore implements it in memory; the Postgres store implements it on the actions table.
type Store interface {
	// Enqueue stores a new pending action. It applies the defaults (NotBefore: now; Deadline:
	// DefaultDeadline), validates it (errors wrap ErrInvalid), and returns Inserted false with the
	// existing ID when an action with the same dedupe key exists, whatever its status. An action
	// whose deadline has already passed isn't stored: Dropped says so.
	Enqueue(ctx context.Context, a Action) (EnqueueResult, error)
	// Get returns one action, or ErrNotFound.
	Get(ctx context.Context, id int64) (Action, error)
	// Cancel moves the pending action with this dedupe key to cancelled, and reports whether
	// there was one. A running or finished action is left alone.
	Cancel(ctx context.Context, dedupeKey, reason string) (bool, error)

	// Lock makes the caller the only executor of bot until unlock is called; it returns
	// ErrExecutorRunning if another executor holds the bot.
	Lock(ctx context.Context, bot wa.JID) (unlock func(), err error)
	// HoldsLock returns nil while this store still holds bot's executor lock, and an error
	// wrapping ErrLockLost once it doesn't (on Postgres: the lock's connection died). The
	// executor checks it before every WhatsApp call.
	HoldsLock(ctx context.Context, bot wa.JID) error
	// Recover returns the bot's running actions to pending, after a crash, and counts them.
	Recover(ctx context.Context, bot wa.JID) (int, error)
	// Candidates returns the bot's pending actions the executor chooses from: for each priority
	// and chat, the n that come first by not_before and id and the n with the newest message
	// time; and for each priority, the n with the earliest deadlines. Each action appears once,
	// ordered by priority, not_before and id. Windowing per chat keeps a raided group's backlog
	// from hiding other groups' work.
	Candidates(ctx context.Context, bot wa.JID, n int) ([]Action, error)
	// Pending returns the bot's pending actions of one kind in one chat, ordered by id; the
	// executor uses it to coalesce kicks.
	Pending(ctx context.Context, bot, chat wa.JID, kind Kind) ([]Action, error)
	// Claim moves a pending action to running and counts the attempt. It returns false when the
	// action is no longer pending (cancelled or expired meanwhile) or is locked by someone else.
	Claim(ctx context.Context, id int64) (Action, bool, error)
	// Finish moves a running action to a final status with its error text and result.
	Finish(ctx context.Context, id int64, status Status, errText string, res Result) error
	// Retry moves a running action back to pending, to run again at r.NotBefore, keeping
	// r.Result (a batch's partial outcomes). Unless r.Attempted, the claim's attempt is given
	// back (the action was held back, or yielded its turn).
	Retry(ctx context.Context, id int64, r RetryInfo) error
	// ExpireDue moves the bot's pending actions whose deadline is at or before now to expired,
	// and returns them.
	ExpireDue(ctx context.Context, bot wa.JID, now time.Time) ([]Action, error)
	// FailPending moves the bot's pending actions of the given kinds in chat to failed.
	FailPending(ctx context.Context, bot, chat wa.JID, kinds []Kind, reason string) (int, error)
	// WritesSince counts the bot's actions that reached WhatsApp (done or failed after a call)
	// since t.
	WritesSince(ctx context.Context, bot wa.JID, since time.Time) (int, error)

	// Control returns the control state of a scope (a bot JID, or GlobalScope).
	Control(ctx context.Context, scope string) (Control, error)
	// UpdateControl changes the control state of a scope atomically and returns the new state.
	UpdateControl(ctx context.Context, scope string, change func(*Control)) (Control, error)
}

// RetryInfo describes how a running action goes back to pending.
type RetryInfo struct {
	NotBefore time.Time
	Error     string
	Called    bool   // a WhatsApp call was made for it this time (counts as a write)
	Attempted bool   // the call it got counts toward Config.MaxAttempts
	Result    Result // outcomes so far (e.g. JIDs of a kick already removed)
}

// GlobalScope is the control scope that applies to every bot: the kill switch lives there.
const GlobalScope = "global"

// Control is the persisted control state of a bot, or of all bots (GlobalScope). The operator
// changes it from another process (the API role), so the executor re-reads it often.
type Control struct {
	Paused   bool // kill switch: no action runs (GlobalScope), or none for this bot
	PausedBy string
	PausedAt time.Time

	BreakerOpen   bool // only lockdowns at P0 run (Config.BreakerAllows)
	BreakerReason string
	BreakerAt     time.Time // when it last opened
	// BreakerClosedAt is when an operator last closed it. An executor only takes a close that is
	// newer than its own opening, so a stale read can't undo a breaker that just opened.
	BreakerClosedAt time.Time

	// Ceiling overrides Config.CeilingPerMin when non-zero. Only the operator changes it.
	Ceiling int
	// Rate is the current, slowed-down write rate per minute; zero means at the ceiling.
	Rate float64
	// LastWarning is the last warning sign; recovery counts calm time from here.
	LastWarning time.Time
	// LastRecovery is the last recovery step.
	LastRecovery time.Time
	// RateLimitedUntil is the end of a 429 pause, so a restart doesn't cut it short.
	RateLimitedUntil time.Time
}
