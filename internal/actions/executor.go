package actions

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Errors returned by Executor.Read.
var (
	// ErrPaused: actions are paused (the kill switch, or a 429 pause).
	ErrPaused = errors.New("actions: paused")
	// ErrBreakerOpen: the bot's circuit breaker is open.
	ErrBreakerOpen = errors.New("actions: circuit breaker open")
)

// Deps are the executor's collaborators.
type Deps struct {
	Bot    wa.JID      // the bot number this executor serves
	Client wa.WAClient // that bot's WhatsApp connection
	Store  Store
	Clock  clock.Clock // clock.Real when nil
	Rand   *rand.Rand  // jitter source; seeded randomly when nil
	Groups Groups      // required: silent mode and admin state
	Alerts Alerter     // operator alerts; logged only when nil
	Log    zerolog.Logger
}

// State is a snapshot of an executor's pacing and control state.
type State struct {
	Ceiling     int     // writes/min the rate never exceeds on its own
	Rate        float64 // the current write rate per minute (at most Ceiling)
	BreakerOpen bool
	Paused      bool      // the kill switch is on
	PausedUntil time.Time // a 429 pause, if one is running
}

// adminKinds are the kinds that fail once the bot is no longer a group admin.
var adminKinds = func() []Kind {
	var out []Kind
	for _, k := range Kinds {
		if k.NeedsAdmin() {
			out = append(out, k)
		}
	}
	return out
}()

// Executor runs one bot's actions, strictly one WhatsApp call at a time, paced under the
// ceiling (DESIGN.md §10). Reads made through Read share the one-call-at-a-time rule but have
// their own budget.
type Executor struct {
	cfg  Config
	d    Deps
	clk  clock.Clock
	log  zerolog.Logger
	wake chan struct{}

	// callMu makes WhatsApp calls strictly serial, reads included.
	callMu sync.Mutex

	// mu guards the pacing and control state below, shared with Read, Warn, OpenBreaker and State.
	mu           sync.Mutex
	rnd          *rand.Rand
	ceiling      int
	rate         float64
	writes       bucket
	reads        bucket
	pausedUntil  time.Time
	killed       bool
	breakerOpen  bool
	breakerAt    time.Time
	lockLost     bool        // the store no longer holds this executor's lock
	warnings     []time.Time // warning signs within Config.BreakerWindow
	lastWarning  time.Time
	lastRecovery time.Time
	lastWrite    time.Time

	// Owned by the Run goroutine.
	unlock    func() // releases the executor lock
	cands     []Action
	chatOut   map[wa.JID]*window // writes per chat
	posts     map[wa.JID]*window // posts per protected, non-silent group
	reserves  map[Purpose]*bucket
	rr        map[Priority]wa.JID // last chat served per priority (round robin)
	postings  map[wa.JID]postingEntry
	lastText  string
	lastChat  wa.JID
	sentAny   bool
	day       time.Time
	dayWrites int
	dayAlert  bool
	polledAt  time.Time
}

type postingEntry struct {
	p   Posting
	err error
	at  time.Time
}

// NewExecutor checks cfg and returns an executor for d.Bot. Call Run to start it.
func NewExecutor(cfg Config, d Deps) (*Executor, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("actions config: %w", err)
	}
	switch {
	case d.Bot == "":
		return nil, errors.New("actions: executor needs a bot")
	case d.Client == nil:
		return nil, errors.New("actions: executor needs a WhatsApp client")
	case d.Store == nil:
		return nil, errors.New("actions: executor needs a store")
	case d.Groups == nil:
		return nil, errors.New("actions: executor needs the group state (silent mode)")
	}
	if d.Clock == nil {
		d.Clock = clock.Real{}
	}
	if d.Rand == nil {
		d.Rand = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())) // jitter, not security
	}
	now := d.Clock.Now()
	e := &Executor{
		cfg:      cfg,
		d:        d,
		clk:      d.Clock,
		log:      d.Log.With().Str("module", "actions").Str("bot", maskJID(d.Bot)).Logger(),
		wake:     make(chan struct{}, 1),
		rnd:      d.Rand,
		ceiling:  cfg.CeilingPerMin,
		rate:     float64(cfg.CeilingPerMin),
		chatOut:  map[wa.JID]*window{},
		posts:    map[wa.JID]*window{},
		reserves: map[Purpose]*bucket{},
		rr:       map[Priority]wa.JID{},
		postings: map[wa.JID]postingEntry{},
	}
	e.writes = newBucket(float64(cfg.Burst), e.rate, now)
	e.reads = newBucket(float64(cfg.ReadBurst), float64(cfg.ReadsPerMin), now)
	if cfg.OTPPerMin > 0 {
		b := newBucket(1, float64(cfg.OTPPerMin), now)
		e.reserves[PurposeOTP] = &b
	}
	if cfg.ReviewPerMin > 0 {
		b := newBucket(1, float64(cfg.ReviewPerMin), now)
		e.reserves[PurposeReview] = &b
	}
	return e, nil
}

// Run executes the bot's actions until ctx is done. It returns ErrExecutorRunning if another
// executor serves the bot, and nil after ctx is cancelled.
func (e *Executor) Run(ctx context.Context) error {
	unlock, err := e.d.Store.Lock(ctx, e.d.Bot)
	if err != nil {
		return err
	}
	e.unlock = unlock
	defer func() {
		if e.unlock != nil {
			e.unlock()
		}
	}()
	if err := e.recoverRunning(ctx); err != nil {
		return err
	}
	if err := e.start(ctx); err != nil {
		return err
	}
	for ctx.Err() == nil {
		if err := e.step(ctx); err != nil {
			if ctx.Err() != nil {
				break
			}
			// A store error is logged and retried after a poll interval; the table stays the
			// truth. An action whose settle write failed may be left running: this executor is
			// the only owner of the bot's running actions, so it puts them back.
			e.log.Error().Err(err).Msg("action executor step failed")
			e.sleep(ctx, e.cfg.PollInterval)
			if e.ownsLock() {
				if err := e.recoverRunning(ctx); err != nil {
					e.log.Error().Err(err).Msg("recover running actions after a store error")
				}
			}
		}
	}
	return nil
}

// recoverRunning returns the bot's running actions to pending (after a crash, a lost lock or a
// failed store write).
func (e *Executor) recoverRunning(ctx context.Context) error {
	n, err := e.d.Store.Recover(ctx, e.d.Bot)
	if err != nil {
		return fmt.Errorf("recover actions: %w", err)
	}
	if n > 0 {
		e.log.Warn().Int("actions", n).Msg("interrupted actions run again")
	}
	return nil
}

func (e *Executor) ownsLock() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.lockLost
}

// checkLock verifies the executor still holds its bot's lock. A lost lock (on Postgres, its
// connection died) stops every call at once, since another process may take over the bot.
func (e *Executor) checkLock(ctx context.Context) bool {
	if err := e.d.Store.HoldsLock(ctx, e.d.Bot); err != nil {
		e.loseLock(err)
		return false
	}
	return true
}

func (e *Executor) loseLock(err error) {
	e.mu.Lock()
	already := e.lockLost
	e.lockLost = true
	e.mu.Unlock()
	if !already {
		e.log.Error().Err(err).Msg("executor lock lost: no WhatsApp calls until it is taken again")
	}
}

// relock tries to take the bot's lock again after losing it. While another executor holds it,
// this one stands by and makes no calls.
func (e *Executor) relock(ctx context.Context) error {
	if e.unlock != nil {
		e.unlock() // release the dead connection
		e.unlock = nil
	}
	unlock, err := e.d.Store.Lock(ctx, e.d.Bot)
	if errors.Is(err, ErrExecutorRunning) {
		return nil // standing by
	}
	if err != nil {
		return fmt.Errorf("take the executor lock again: %w", err)
	}
	e.unlock = unlock
	if err := e.recoverRunning(ctx); err != nil {
		return err
	}
	e.mu.Lock()
	e.lockLost = false
	e.mu.Unlock()
	e.log.Warn().Msg("executor lock taken again")
	return nil
}

// start loads the persisted control state and today's write count.
func (e *Executor) start(ctx context.Context) error {
	c, err := e.d.Store.Control(ctx, string(e.d.Bot))
	if err != nil {
		return fmt.Errorf("load control state: %w", err)
	}
	now := e.clk.Now()
	e.mu.Lock()
	// Merge rather than overwrite: a warning sign or breaker opened before Run got here stands.
	if c.Ceiling > 0 {
		e.ceiling = c.Ceiling
	}
	if e.lastWarning.IsZero() {
		e.rate = float64(e.ceiling) // nothing slowed it yet; a zero persisted rate means at the ceiling
	}
	e.rate = math.Min(e.rate, float64(e.ceiling))
	if c.Rate > 0 {
		e.rate = math.Min(e.rate, c.Rate)
	}
	if c.LastWarning.After(e.lastWarning) {
		e.lastWarning = c.LastWarning
	}
	if c.LastRecovery.After(e.lastRecovery) {
		e.lastRecovery = c.LastRecovery
	}
	if c.BreakerOpen && !e.breakerOpen {
		e.breakerOpen, e.breakerAt = true, c.BreakerAt
	}
	if c.RateLimitedUntil.After(e.pausedUntil) {
		e.pausedUntil = c.RateLimitedUntil
	}
	e.resize(now)
	e.mu.Unlock()
	e.day = utcDay(now)
	n, err := e.d.Store.WritesSince(ctx, e.d.Bot, e.day)
	if err != nil {
		return fmt.Errorf("count today's writes: %w", err)
	}
	e.dayWrites, e.dayAlert = n, e.cfg.DailyAlert > 0 && n > e.cfg.DailyAlert
	return e.poll(ctx)
}

// Wake tells the executor that actions were enqueued or cancelled, so it re-reads the table
// before its next poll.
func (e *Executor) Wake() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// State returns the executor's current pacing and control state.
func (e *Executor) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.recover(e.clk.Now())
	return State{
		Ceiling:     e.ceiling,
		Rate:        e.rate,
		BreakerOpen: e.breakerOpen,
		Paused:      e.killed,
		PausedUntil: e.pausedUntil,
	}
}

// step runs at most one action (or one coalesced batch), or waits until one may run.
func (e *Executor) step(ctx context.Context) error {
	now := e.clk.Now()
	if now.Sub(e.polledAt) >= e.cfg.PollInterval || e.cands == nil {
		if err := e.poll(ctx); err != nil {
			return err
		}
	}
	if d := e.blocked(now); d > 0 {
		e.wait(ctx, d)
		return nil
	}
	// The write token comes first, so pick only spends a reserved sub-budget on an action that
	// runs at once.
	e.mu.Lock()
	tokenWait := e.writes.wait(now)
	e.mu.Unlock()
	if tokenWait > 0 {
		e.wait(ctx, tokenWait)
		return nil
	}
	a, wait := e.pick(ctx, now)
	if a == nil {
		e.wait(ctx, wait)
		return nil
	}
	err := e.execute(ctx, *a)
	e.cands = nil // re-read the table: the executed action and its batch changed status
	return err
}

// poll re-reads the control state, expires overdue actions and refills the candidate window.
func (e *Executor) poll(ctx context.Context) error {
	now := e.clk.Now()
	if !e.ownsLock() {
		if err := e.relock(ctx); err != nil {
			return err
		}
	} else {
		e.checkLock(ctx)
	}
	if err := e.refreshControl(ctx); err != nil {
		return err
	}
	expired, err := e.d.Store.ExpireDue(ctx, e.d.Bot, now)
	if err != nil {
		return fmt.Errorf("expire actions: %w", err)
	}
	for _, a := range expired {
		e.log.Info().Int64("action", a.ID).Str("kind", string(a.Kind)).Stringer("priority", a.Priority).
			Time("deadline", a.Deadline).Msg("action expired before it could run")
	}
	cands, err := e.d.Store.Candidates(ctx, e.d.Bot, e.cfg.Window)
	if err != nil {
		return fmt.Errorf("read pending actions: %w", err)
	}
	if cands == nil {
		cands = []Action{}
	}
	e.cands, e.polledAt = cands, now
	return nil
}

// refreshControl re-reads the kill switch and the bot's control state (operator changes made
// from another process).
func (e *Executor) refreshControl(ctx context.Context) error {
	now := e.clk.Now()
	global, err := e.d.Store.Control(ctx, GlobalScope)
	if err != nil {
		return fmt.Errorf("read kill switch: %w", err)
	}
	own, err := e.d.Store.Control(ctx, string(e.d.Bot))
	if err != nil {
		return fmt.Errorf("read control state: %w", err)
	}
	e.mu.Lock()
	changed := e.applyControl(own, now)
	e.killed = global.Paused || own.Paused
	e.mu.Unlock()
	if changed {
		e.persistRate(ctx)
	}
	return nil
}

// applyControl takes the operator-owned parts of the control state and reports whether the
// ceiling changed. It must be called with e.mu held.
func (e *Executor) applyControl(c Control, now time.Time) bool {
	ceiling := e.cfg.CeilingPerMin
	if c.Ceiling > 0 {
		ceiling = c.Ceiling
	}
	changed := ceiling != e.ceiling
	if changed {
		e.recover(now)
		e.log.Warn().Int("from", e.ceiling).Int("to", ceiling).Msg("write ceiling changed by the operator")
		e.ceiling = ceiling
		if e.rate >= float64(ceiling) {
			e.rate = float64(ceiling)
		} else {
			// A raise doesn't make the rate jump: it recovers from here, one step per calm period.
			e.lastRecovery = now
		}
		e.resize(now)
	}
	switch {
	case c.BreakerOpen && !e.breakerOpen:
		e.breakerOpen, e.breakerAt = true, c.BreakerAt
	case !c.BreakerOpen && e.breakerOpen && !c.BreakerClosedAt.IsZero() && !c.BreakerClosedAt.Before(e.breakerAt):
		e.log.Warn().Msg("circuit breaker closed by the operator")
		e.breakerOpen = false
		e.warnings = nil
	}
	return changed
}

// blocked returns how long nothing at all may run: the kill switch or a 429 pause.
func (e *Executor) blocked(now time.Time) time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.killed || e.lockLost {
		return e.cfg.PollInterval
	}
	if now.Before(e.pausedUntil) {
		return e.pausedUntil.Sub(now)
	}
	return 0
}

// wait sleeps for d (at most a poll interval) or until Wake, whichever comes first.
func (e *Executor) wait(ctx context.Context, d time.Duration) {
	d = min(max(d, time.Millisecond), e.cfg.PollInterval)
	select {
	case <-ctx.Done():
	case <-e.wake:
		e.cands = nil
	case <-e.clk.After(d):
	}
}

// sleep waits for d or until ctx is done; Wake doesn't cut it short.
func (e *Executor) sleep(ctx context.Context, d time.Duration) {
	if d > 0 {
		_ = e.clk.Sleep(ctx, d)
	}
}

// pick chooses the next action to run, or says how long to wait before something may be ready.
func (e *Executor) pick(ctx context.Context, now time.Time) (*Action, time.Duration) {
	next := e.cfg.PollInterval
	later := func(d time.Duration) {
		if d > 0 && d < next {
			next = d
		}
	}
	e.mu.Lock()
	breaker := e.breakerOpen
	e.mu.Unlock()

	byPriority := map[Priority][]*Action{}
	for i := range e.cands {
		a := &e.cands[i]
		if !a.Deadline.After(now) {
			continue // ExpireDue drops it on the next poll
		}
		if breaker && !e.allowedWhenOpen(*a) {
			continue
		}
		if d := a.NotBefore.Sub(now); d > 0 {
			later(d)
			continue
		}
		if a.Kind.Coalesced() {
			if d := a.CreatedAt.Add(e.cfg.KickWindow).Sub(now); d > 0 {
				later(d)
				continue
			}
		}
		if d := e.chatWindow(a.Chat).wait(now); d > 0 {
			later(d)
			continue
		}
		if a.Kind.Posts() && a.Chat.IsGroup() {
			if p := e.posting(ctx, a.Chat, now); p.err == nil && p.p.Protected && !p.p.Silent {
				if d := e.postWindow(a.Chat).wait(now); d > 0 {
					later(d)
					continue
				}
			}
		}
		if a.Kind == KindSend {
			if _, ok := e.chooseText(*a); !ok {
				continue // waits for a different text to go out first
			}
		}
		byPriority[a.Priority] = append(byPriority[a.Priority], a)
	}

	// P0 always comes first; then OTPs and reviews may use their reserved sub-budgets to run
	// ahead of P1 and P2 work, so a raid can't starve them.
	if len(byPriority[P0]) > 0 {
		return e.within(P0, byPriority[P0], now), 0
	}
	if best := lowest(byPriority); best > P0 && best < P3 {
		for _, purpose := range []Purpose{PurposeOTP, PurposeReview} {
			r := e.reserves[purpose]
			if r == nil {
				continue
			}
			var ready []*Action
			for _, a := range byPriority[P3] {
				if a.Kind == KindSend && a.Payload.Purpose == purpose {
					ready = append(ready, a)
				}
			}
			if len(ready) > 0 && r.take(now) {
				return e.within(P3, ready, now), 0
			}
		}
	}
	for p := P0; p <= P4; p++ {
		if len(byPriority[p]) > 0 {
			return e.within(p, byPriority[p], now), 0
		}
	}
	return nil, next
}

func lowest(byPriority map[Priority][]*Action) Priority {
	for p := P0; p <= P4; p++ {
		if len(byPriority[p]) > 0 {
			return p
		}
	}
	return P4 + 1
}

// within orders the ready actions of one priority: urgent ones (deadline within UrgentWithin)
// first, earliest deadline first; otherwise round robin across chats, and within a chat
// lockdowns, then deletes newest first, then kicks, then the rest by age.
func (e *Executor) within(p Priority, ready []*Action, now time.Time) *Action {
	var urgent *Action
	for _, a := range ready {
		if a.Deadline.Sub(now) <= e.cfg.UrgentWithin {
			if urgent == nil || a.Deadline.Before(urgent.Deadline) || (a.Deadline.Equal(urgent.Deadline) && a.ID < urgent.ID) {
				urgent = a
			}
		}
	}
	if urgent != nil {
		return urgent
	}
	chats := make([]wa.JID, 0, len(ready))
	for _, a := range ready {
		if !slices.Contains(chats, a.Chat) {
			chats = append(chats, a.Chat)
		}
	}
	slices.Sort(chats)
	chat := chats[0]
	if last, ok := e.rr[p]; ok {
		if i, found := slices.BinarySearch(chats, last); found {
			chat = chats[(i+1)%len(chats)]
		} else if i < len(chats) {
			chat = chats[i]
		}
	}
	var best *Action
	for _, a := range ready {
		if a.Chat == chat && (best == nil || before(a, best)) {
			best = a
		}
	}
	return best
}

// before orders two actions of the same chat and priority.
func before(a, b *Action) bool {
	if ra, rb := rank(a.Kind), rank(b.Kind); ra != rb {
		return ra < rb
	}
	if a.Kind == KindRevoke {
		ta, tb := visible(a), visible(b)
		if !ta.Equal(tb) {
			return ta.After(tb) // newest first: that's what members see
		}
	}
	return a.ID < b.ID
}

func rank(k Kind) int {
	switch {
	case k.Lockdown():
		return 0
	case k == KindRevoke:
		return 1
	case k.Coalesced():
		return 2
	default:
		return 3
	}
}

func visible(a *Action) time.Time {
	if a.MessageTime.IsZero() {
		return a.CreatedAt
	}
	return a.MessageTime
}

func (e *Executor) allowedWhenOpen(a Action) bool {
	return a.Priority == P0 && slices.Contains(e.cfg.BreakerAllows, a.Kind)
}

func (e *Executor) chatWindow(chat wa.JID) *window {
	w := e.chatOut[chat]
	if w == nil {
		w = &window{limit: e.cfg.GroupPerMin, span: time.Minute}
		e.chatOut[chat] = w
	}
	return w
}

func (e *Executor) postWindow(chat wa.JID) *window {
	w := e.posts[chat]
	if w == nil {
		w = &window{limit: e.cfg.PostsPerGroupPerMin, span: time.Minute}
		e.posts[chat] = w
	}
	return w
}

// posting returns the chat's posting settings, cached for a poll interval. Only pick uses the
// cache; execute always asks again before posting.
func (e *Executor) posting(ctx context.Context, chat wa.JID, now time.Time) postingEntry {
	if p, ok := e.postings[chat]; ok && now.Sub(p.at) < e.cfg.PollInterval {
		return p
	}
	p, err := e.d.Groups.Posting(ctx, chat)
	entry := postingEntry{p: p, err: err, at: now}
	e.postings[chat] = entry
	return entry
}

// chooseText picks the text of a send: never the previous send's text when the chat differs
// (DESIGN.md §10.3). It reports false when no wording is allowed yet.
func (e *Executor) chooseText(a Action) (string, bool) {
	if a.Payload.Text == "" && len(a.Payload.Variants) == 0 {
		return "", true // an image without a caption: there is no text to repeat
	}
	var texts []string
	for _, t := range append([]string{a.Payload.Text}, a.Payload.Variants...) {
		if t != "" && !slices.Contains(texts, t) {
			texts = append(texts, t)
		}
	}
	if len(texts) == 0 {
		return "", true
	}
	if !e.sentAny || a.Chat == e.lastChat {
		return texts[0], true
	}
	allowed := slices.DeleteFunc(texts, func(t string) bool { return t == e.lastText })
	switch len(allowed) {
	case 0:
		return "", false
	case 1:
		return allowed[0], true
	default:
		e.mu.Lock()
		i := e.rnd.IntN(len(allowed))
		e.mu.Unlock()
		return allowed[i], true
	}
}

func utcDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
