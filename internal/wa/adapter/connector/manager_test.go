package connector

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// memLease mimics the Postgres row lease on a shared clock.
type memLease struct {
	mu       sync.Mutex
	clk      clock.Clock
	owner    map[wa.JID]string
	until    map[wa.JID]time.Time
	renewErr error
}

func newMemLease(clk clock.Clock) *memLease {
	return &memLease{clk: clk, owner: map[wa.JID]string{}, until: map[wa.JID]time.Time{}}
}

func (l *memLease) Acquire(_ context.Context, bot wa.JID, owner string, ttl time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clk.Now()
	if o := l.owner[bot]; o != "" && o != owner && now.Before(l.until[bot]) {
		return false, nil
	}
	l.owner[bot], l.until[bot] = owner, now.Add(ttl)
	return true, nil
}

func (l *memLease) Renew(_ context.Context, bot wa.JID, owner string, ttl time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.renewErr != nil {
		return false, l.renewErr
	}
	if l.owner[bot] != owner {
		return false, nil
	}
	l.until[bot] = l.clk.Now().Add(ttl)
	return true, nil
}

func (l *memLease) Release(_ context.Context, bot wa.JID, owner string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owner[bot] == owner {
		delete(l.owner, bot)
	}
	return nil
}

func (l *memLease) steal(bot wa.JID, owner string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.owner[bot] = owner
}

type memRegistry struct {
	mu   sync.Mutex
	bots map[wa.JID]Bot
}

func (r *memRegistry) UpsertBot(_ context.Context, b Bot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.bots[b.JID]; ok {
		b.Status = old.Status
	}
	r.bots[b.JID] = b
	return nil
}

func (r *memRegistry) SetBotStatus(_ context.Context, bot wa.JID, s BotStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.bots[bot]
	b.Status = s
	r.bots[bot] = b
	return nil
}

func (r *memRegistry) Bot(_ context.Context, bot wa.JID) (Bot, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.bots[bot]
	return b, ok, nil
}

func (r *memRegistry) status(bot wa.JID) BotStatus {
	b, _, _ := r.Bot(context.Background(), bot)
	return b.Status
}

type fakeSession struct {
	mu        sync.Mutex
	connected bool
	sink      wa.EventSink
}

func (s *fakeSession) Connect(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = true
	return nil
}

func (s *fakeSession) Disconnect() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = false
}

func (s *fakeSession) Client() wa.WAClient { return nil }

func (s *fakeSession) isConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

type fakeBackend struct {
	mu       sync.Mutex
	devices  []Device
	sessions []*fakeSession
}

func (b *fakeBackend) Devices(context.Context) ([]Device, error) { return b.devices, nil }

func (b *fakeBackend) Open(_ context.Context, _ Device, sink wa.EventSink) (Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &fakeSession{sink: sink}
	b.sessions = append(b.sessions, s)
	return s, nil
}

func (b *fakeBackend) last() *fakeSession {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.sessions) == 0 {
		return nil
	}
	return b.sessions[len(b.sessions)-1]
}

func (b *fakeBackend) live() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, s := range b.sessions {
		if s.isConnected() {
			n++
		}
	}
	return n
}

type env struct {
	clk     *clock.Fake
	lease   *memLease
	reg     *memRegistry
	backend *fakeBackend
	sinkErr error
	got     []wa.Event
	mu      sync.Mutex
}

func newEnv() *env {
	clk := clock.NewFake(t0)
	return &env{
		clk: clk, lease: newMemLease(clk), reg: &memRegistry{bots: map[wa.JID]Bot{}},
		backend: &fakeBackend{devices: []Device{{JID: bot, LID: "900@lid", PushName: "Assistant"}}},
	}
}

func (e *env) manager(t *testing.T, owner string) *Manager {
	t.Helper()
	m, err := NewManager(e.backend, e.lease, e.reg, wa.EventSinkFunc(func(_ context.Context, evt wa.Event) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.got = append(e.got, evt)
		return e.sinkErr
	}), Options{Owner: owner, Clock: e.clk, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNewManagerValidates(t *testing.T) {
	e := newEnv()
	if _, err := NewManager(e.backend, e.lease, e.reg, nil, Options{}); err == nil {
		t.Error("no owner: nil error")
	}
	if _, err := NewManager(e.backend, e.lease, e.reg, nil, Options{Owner: "a", RenewEvery: time.Minute, LeaseTTL: time.Second}); err == nil {
		t.Error("TTL shorter than the renew interval: nil error (the lease would lapse while held)")
	}
}

func TestOnlyOneProcessConnectsABot(t *testing.T) {
	e := newEnv()
	ctx := t.Context()
	a, b := e.manager(t, "a"), e.manager(t, "b")
	a.Tick(ctx)
	b.Tick(ctx)
	if len(a.Connected()) != 1 || len(b.Connected()) != 0 || e.backend.live() != 1 {
		t.Fatalf("a=%v b=%v live=%d: want exactly one process connected (a second connection is StreamReplaced)", a.Connected(), b.Connected(), e.backend.live())
	}
	if got := e.reg.status(bot); got != BotActive {
		t.Errorf("bot status %s, want active after recording the device", got)
	}
	if r, _, _ := e.reg.Bot(ctx, bot); r.LID != "900@lid" || r.DisplayName != "Assistant" {
		t.Errorf("bots row %+v: want the device's LID and name", r)
	}

	// a keeps renewing; b still can't take it after the TTL.
	for range 5 {
		e.clk.Advance(DefaultRenewEvery)
		a.Tick(ctx)
		b.Tick(ctx)
	}
	if len(b.Connected()) != 0 || e.backend.live() != 1 {
		t.Fatal("b connected while a was renewing")
	}

	// a shuts down: its lease is released and b takes over on its next round.
	ctxA, cancel := context.WithCancel(ctx)
	cancel()
	_ = a.Run(ctxA)
	b.Tick(ctx)
	if len(b.Connected()) != 1 || e.backend.live() != 1 {
		t.Errorf("after a stopped: b=%v live=%d, want b connected", b.Connected(), e.backend.live())
	}
}

func TestLostLeaseDisconnects(t *testing.T) {
	for _, tc := range []struct {
		name string
		lose func(e *env)
		want wa.ConnectionState
	}{
		{"taken by another owner", func(e *env) { e.lease.steal(bot, "intruder") }, wa.Released},
		{"renew error", func(e *env) { e.lease.renewErr = errors.New("db down") }, wa.Disconnected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv()
			m := e.manager(t, "a")
			m.Tick(t.Context())
			tc.lose(e)
			m.Tick(t.Context())
			if len(m.Connected()) != 0 || e.backend.last().isConnected() {
				t.Errorf("still connected after losing the lease; want an immediate disconnect")
			}
			last, ok := e.got[len(e.got)-1].(*wa.ConnectionEvent)
			if !ok || last.State != tc.want {
				t.Errorf("last event %+v, want ConnectionEvent %s (whatsmeow reports no manual disconnect, so health must hear it from us)", e.got[len(e.got)-1], tc.want)
			}
		})
	}
}

func TestPausedBotIsNeverConnected(t *testing.T) {
	e := newEnv()
	e.reg.bots[bot] = Bot{JID: bot, Status: BotPaused}
	m := e.manager(t, "a")
	m.Tick(t.Context())
	if len(m.Connected()) != 0 {
		t.Error("paused bot connected")
	}
	if e.lease.owner[bot] != "" {
		t.Error("paused bot's lease taken")
	}
}

func TestAccountEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		m := e.manager(t, "a")
		ctx := context.Background()
		m.Tick(ctx)
		sink := e.backend.last().sink

		_ = sink.HandleEvent(ctx, &wa.AccountEvent{Bot: bot, Problem: wa.ProblemTemporaryBan, Code: 101})
		if s := e.reg.status(bot); s != BotBanned {
			t.Errorf("status %s after a temporary ban, want banned", s)
		}
		_ = sink.HandleEvent(ctx, &wa.ConnectionEvent{Bot: bot, State: wa.Connected})
		if s := e.reg.status(bot); s != BotActive {
			t.Errorf("status %s after reconnecting, want active", s)
		}

		_ = sink.HandleEvent(ctx, &wa.AccountEvent{Bot: bot, Problem: wa.ProblemStreamReplaced})
		synctest.Wait()
		if len(m.Connected()) != 0 || e.lease.owner[bot] != "" {
			t.Fatalf("after StreamReplaced: connected=%v lease=%q, want dropped and released", m.Connected(), e.lease.owner[bot])
		}
		m.Tick(ctx)
		if len(m.Connected()) != 0 {
			t.Error("reconnected right after StreamReplaced, want a cooldown (no kick-off loop)")
		}
		e.clk.Advance(replacedCooldown)
		m.Tick(ctx)
		if len(m.Connected()) != 1 {
			t.Error("not reconnected after the cooldown")
		}

		sink = e.backend.last().sink
		_ = sink.HandleEvent(ctx, &wa.AccountEvent{Bot: bot, Problem: wa.ProblemLoggedOut})
		synctest.Wait()
		if s := e.reg.status(bot); s != BotUnpaired || len(m.Connected()) != 0 {
			t.Errorf("after logout: status %s connected %v, want unpaired and dropped", s, m.Connected())
		}
		var states []wa.ConnectionState
		for _, ev := range e.got {
			if c, ok := ev.(*wa.ConnectionEvent); ok {
				states = append(states, c.State)
			}
		}
		if len(e.got) != 6 || len(states) != 3 || states[1] != wa.Released || states[2] != wa.Released {
			t.Errorf("sink got %d events, connection states %v; want every event passed on plus a Released for each drop", len(e.got), states)
		}
	})
}

func TestMessageStoreErrorReachesTheAck(t *testing.T) {
	e := newEnv()
	e.sinkErr = errors.New("inbox down")
	m := e.manager(t, "a")
	m.Tick(t.Context())
	err := e.backend.last().sink.HandleEvent(t.Context(), &wa.MessageEvent{Bot: bot})
	if err == nil {
		t.Error("sink error swallowed; the message would be acked without being stored")
	}
}

func TestMask(t *testing.T) {
	if got := mask("966512345678@s.whatsapp.net"); got != "966*******78@s.whatsapp.net" {
		t.Errorf("mask = %s", got)
	}
	if got := mask("1@lid"); got != "1@lid" {
		t.Errorf("mask(lid) = %s", got)
	}
}
