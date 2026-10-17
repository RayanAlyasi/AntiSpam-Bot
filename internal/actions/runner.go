package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Connector is what the Runner needs from the WhatsApp connector: which bots are connected in
// this process, and each one's client.
type Connector interface {
	Connected() []wa.JID
	Client(bot wa.JID) (wa.WAClient, bool)
}

// RunnerDeps are the Runner's collaborators, shared by every executor it starts.
type RunnerDeps struct {
	Connector  Connector
	Store      Store
	Groups     Groups
	Alerts     Alerter
	OnFinished func(ctx context.Context, a Action) error
	Clock      clock.Clock // clock.Real when nil
	Log        zerolog.Logger
}

// RunnerSync is how often the Runner looks for bots that connected or went away.
const RunnerSync = 5 * time.Second

// Runner keeps exactly one executor running for every bot connected in this process (DESIGN.md
// §10.1): it starts one when a bot connects (or gets a new client), stops it when the bot goes
// away, and retries one that couldn't take its lock because another process owns the bot. It
// also takes the connector's signals: account problems open the bot's breaker (it implements
// the alert package's Breaker), and disconnects shortly after writes slow the bot down.
type Runner struct {
	cfg Config
	d   RunnerDeps
	clk clock.Clock

	mu     sync.Mutex
	execs  map[wa.JID]*running
	outage map[wa.JID]bool // a disconnect warning was counted for the current outage
}

type running struct {
	ex     *Executor
	client wa.WAClient
	cancel context.CancelFunc
	done   chan struct{}
}

// NewRunner checks cfg and returns a Runner. Call Run to start it.
func NewRunner(cfg Config, d RunnerDeps) (*Runner, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("actions config: %w", err)
	}
	if d.Connector == nil || d.Store == nil || d.Groups == nil {
		return nil, errors.New("actions: runner needs a connector, a store and the group state")
	}
	clk := d.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	return &Runner{cfg: cfg, d: d, clk: clk, execs: map[wa.JID]*running{}, outage: map[wa.JID]bool{}}, nil
}

// Run keeps the executors in step with the connected bots until ctx is done, then stops them all.
func (r *Runner) Run(ctx context.Context) error {
	defer r.stopAll()
	for {
		r.sync(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-r.clk.After(RunnerSync):
		}
	}
}

// sync starts executors for newly connected bots and stops the ones whose bot went away or got
// a new client.
func (r *Runner) sync(ctx context.Context) {
	want := map[wa.JID]wa.WAClient{}
	for _, bot := range r.d.Connector.Connected() {
		if c, ok := r.d.Connector.Client(bot); ok {
			want[bot] = c
		}
	}
	r.mu.Lock()
	var stop []*running
	for bot, run := range r.execs {
		select {
		case <-run.done: // stopped by itself (lock held elsewhere, or an error): start again below
			delete(r.execs, bot)
			continue
		default:
		}
		if c, ok := want[bot]; !ok || !sameClient(c, run.client) {
			stop = append(stop, run)
			delete(r.execs, bot)
		}
	}
	r.mu.Unlock()
	for _, run := range stop {
		run.cancel()
		<-run.done
	}
	for bot, c := range want {
		r.mu.Lock()
		_, have := r.execs[bot]
		r.mu.Unlock()
		if !have {
			r.start(ctx, bot, c)
		}
	}
}

func (r *Runner) start(ctx context.Context, bot wa.JID, c wa.WAClient) {
	log := r.d.Log.With().Str("bot", maskJID(bot)).Logger()
	ex, err := NewExecutor(r.cfg, Deps{
		Bot: bot, Client: c, Store: r.d.Store, Clock: r.d.Clock, Groups: r.d.Groups, Alerts: r.d.Alerts,
		OnFinished: r.d.OnFinished, Log: r.d.Log,
	})
	if err != nil {
		log.Error().Err(err).Msg("create action executor")
		return
	}
	ectx, cancel := context.WithCancel(ctx)
	run := &running{ex: ex, client: c, cancel: cancel, done: make(chan struct{})}
	r.mu.Lock()
	r.execs[bot] = run
	r.mu.Unlock()
	go func() {
		defer close(run.done)
		switch err := ex.Run(ectx); {
		case errors.Is(err, ErrExecutorRunning):
			log.Info().Msg("another process runs this bot's actions; retrying later")
		case err != nil:
			log.Error().Err(err).Msg("action executor stopped")
		}
	}()
}

func (r *Runner) stopAll() {
	r.mu.Lock()
	runs := make([]*running, 0, len(r.execs))
	for bot, run := range r.execs {
		runs = append(runs, run)
		delete(r.execs, bot)
	}
	r.mu.Unlock()
	for _, run := range runs {
		run.cancel()
		<-run.done
	}
}

// Executor returns the running executor of bot, for reads (Executor.Read) and wake-ups. An
// executor that stopped by itself, or that never took its lock (another process runs the bot),
// isn't returned.
func (r *Runner) Executor(bot wa.JID) (*Executor, bool) {
	r.mu.Lock()
	run, ok := r.execs[bot]
	r.mu.Unlock()
	if !ok {
		return nil, false
	}
	select {
	case <-run.done:
		return nil, false
	default:
	}
	if !run.ex.holdsLock() {
		return nil, false
	}
	return run.ex, true
}

// Wake tells bot's executor that actions were enqueued, if it runs here.
func (r *Runner) Wake(bot wa.JID) {
	if ex, ok := r.Executor(bot); ok {
		ex.Wake()
	}
}

// Trip opens bot's circuit breaker after an account problem (a temporary ban, a logout or a
// replaced stream; DESIGN.md §10.5). It implements the alert package's Breaker. The breaker
// stays open until an operator closes it, whatever until says (it is only logged), and it is
// persisted, so it holds even when the bot's executor runs in another process or starts later.
func (r *Runner) Trip(ctx context.Context, bot wa.JID, reason wa.AccountProblem, until time.Time) error {
	text := string(reason)
	if !until.IsZero() {
		text = fmt.Sprintf("%s until %s", reason, until.UTC().Format(time.RFC3339))
	}
	if ex, ok := r.Executor(bot); ok {
		return ex.OpenBreaker(ctx, text)
	}
	now := r.clk.Now()
	if _, err := r.d.Store.UpdateControl(ctx, string(bot), func(c *Control) {
		if !c.BreakerOpen {
			c.BreakerOpen, c.BreakerReason, c.BreakerAt = true, text, now
		}
	}); err != nil {
		return fmt.Errorf("open breaker of %s: %w", maskJID(bot), err)
	}
	r.d.Log.Error().Str("bot", maskJID(bot)).Str("reason", text).Msg("circuit breaker open: only P0 lockdowns run until an operator closes it")
	return nil
}

// HandleEvent takes the connector's connection events: a disconnect or a keepalive timeout
// shortly after a write is a warning sign that slows the bot down (DESIGN.md §10.3). Other
// events are ignored. It never blocks on the network for long, so the connector may call it on
// its receive loop.
func (r *Runner) HandleEvent(ctx context.Context, evt wa.Event) error {
	ce, ok := evt.(*wa.ConnectionEvent)
	if !ok {
		return nil
	}
	switch ce.State {
	case wa.Disconnected, wa.KeepAliveTimeout:
		// One outage is one warning sign, however many timeouts and disconnects it produces;
		// otherwise a single network blip after a write would open the breaker.
		r.mu.Lock()
		counted := r.outage[ce.Bot]
		r.outage[ce.Bot] = true
		r.mu.Unlock()
		if ex, ok := r.Executor(ce.Bot); ok && !counted {
			ex.Warn(ctx, WarnDisconnect)
		}
	case wa.Connected, wa.KeepAliveRestored, wa.Released:
		r.mu.Lock()
		delete(r.outage, ce.Bot)
		r.mu.Unlock()
	}
	return nil
}

// sameClient reports whether two clients are the same connection. Clients whose type can't be
// compared are taken to be the same, so the executor isn't restarted on every sync.
func sameClient(a, b wa.WAClient) (same bool) {
	if t := reflect.TypeOf(a); t == nil || t != reflect.TypeOf(b) || !t.Comparable() {
		return t != nil && t == reflect.TypeOf(b)
	}
	// A comparable struct can still hold uncomparable values in interface fields.
	defer func() {
		if recover() != nil {
			same = true
		}
	}()
	return a == b
}
