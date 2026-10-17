package actions_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

const bot wa.JID = "100@s.whatsapp.net"

// call is one WhatsApp write seen by timedClient, stamped with the bubble's time.
type call struct {
	method string
	chat   wa.JID
	at     time.Time
	jids   []wa.JID
	text   string
}

// timedClient wraps the fake client, stamps every write and checks that calls never overlap.
type timedClient struct {
	*fake.Client
	mu       sync.Mutex
	calls    []call
	inflight atomic.Int32
	overlap  atomic.Bool
	delay    time.Duration // how long each write takes
}

func newTimed() *timedClient { return &timedClient{Client: fake.New()} }

func (c *timedClient) record(method string, chat wa.JID, jids []wa.JID, text string) func() {
	if c.inflight.Add(1) > 1 {
		c.overlap.Store(true)
	}
	c.mu.Lock()
	c.calls = append(c.calls, call{method: method, chat: chat, at: time.Now(), jids: jids, text: text})
	c.mu.Unlock()
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	return func() { c.inflight.Add(-1) }
}

func (c *timedClient) writes() []call {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]call(nil), c.calls...)
}

func (c *timedClient) Revoke(ctx context.Context, chat wa.JID, target wa.MessageRef) error {
	defer c.record(fake.MethodRevoke, chat, nil, string(target.ID))()
	return c.Client.Revoke(ctx, chat, target)
}

func (c *timedClient) Send(ctx context.Context, chat wa.JID, msg wa.OutgoingMessage) (wa.SendResult, error) {
	defer c.record(fake.MethodSend, chat, nil, msg.Text)()
	return c.Client.Send(ctx, chat, msg)
}

func (c *timedClient) Edit(ctx context.Context, chat wa.JID, id wa.MessageID, text string) error {
	defer c.record(fake.MethodEdit, chat, nil, text)()
	return c.Client.Edit(ctx, chat, id, text)
}

func (c *timedClient) React(ctx context.Context, chat wa.JID, target wa.MessageRef, emoji string) error {
	defer c.record(fake.MethodReact, chat, nil, emoji)()
	return c.Client.React(ctx, chat, target, emoji)
}

func (c *timedClient) UpdateParticipants(ctx context.Context, group wa.JID, jids []wa.JID, action wa.ParticipantAction) ([]wa.ParticipantResult, error) {
	defer c.record(fake.MethodUpdateParticipants, group, append([]wa.JID(nil), jids...), "")()
	return c.Client.UpdateParticipants(ctx, group, jids, action)
}

func (c *timedClient) UpdateJoinRequests(ctx context.Context, group wa.JID, jids []wa.JID, action wa.JoinRequestAction) ([]wa.ParticipantResult, error) {
	defer c.record(fake.MethodUpdateJoinRequests, group, append([]wa.JID(nil), jids...), "")()
	return c.Client.UpdateJoinRequests(ctx, group, jids, action)
}

func (c *timedClient) SetAnnounce(ctx context.Context, group wa.JID, on bool) error {
	defer c.record(fake.MethodSetAnnounce, group, nil, "")()
	return c.Client.SetAnnounce(ctx, group, on)
}

// groups is a Groups double: every group is protected; silent ones are listed.
type groups struct {
	mu        sync.Mutex
	silent    map[wa.JID]bool
	open      map[wa.JID]bool // not protected
	notAdmin  []wa.JID
	postingFn func(wa.JID) (actions.Posting, error)
}

func newGroups() *groups { return &groups{silent: map[wa.JID]bool{}, open: map[wa.JID]bool{}} }

func (g *groups) Posting(_ context.Context, chat wa.JID) (actions.Posting, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.postingFn != nil {
		return g.postingFn(chat)
	}
	if !chat.IsGroup() || g.open[chat] {
		return actions.Posting{}, nil
	}
	return actions.Posting{Protected: true, Silent: g.silent[chat]}, nil
}

func (g *groups) NotAdmin(_ context.Context, _, group wa.JID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.notAdmin = append(g.notAdmin, group)
	return nil
}

type alerts struct {
	mu  sync.Mutex
	got []actions.Alert
}

func (a *alerts) Alert(_ context.Context, al actions.Alert) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.got = append(a.got, al)
	return nil
}

func (a *alerts) kinds() []actions.AlertKind {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []actions.AlertKind
	for _, al := range a.got {
		out = append(out, al.Kind)
	}
	return out
}

type audits struct {
	mu  sync.Mutex
	got []actions.AuditEntry
}

func (a *audits) Audit(_ context.Context, e actions.AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.got = append(a.got, e)
	return nil
}

// rig is one bot's executor with its doubles, inside a synctest bubble.
type rig struct {
	t      *testing.T
	store  *actions.MemStore
	client *timedClient
	groups *groups
	alerts *alerts
	ex     *actions.Executor
	cancel context.CancelFunc
	done   chan error
}

func newRig(t *testing.T, cfg actions.Config) *rig {
	t.Helper()
	r := &rig{t: t, store: actions.NewMemStore(clock.Real{}), client: newTimed(), groups: newGroups(), alerts: &alerts{}}
	ex, err := actions.NewExecutor(cfg, actions.Deps{
		Bot: bot, Client: r.client, Store: r.store, Groups: r.groups, Alerts: r.alerts,
		Rand: rand.New(rand.NewPCG(1, 2)),
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	r.ex = ex
	return r
}

func (r *rig) start() {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel, r.done = cancel, make(chan error, 1)
	go func() { r.done <- r.ex.Run(ctx) }()
	r.t.Cleanup(r.stop)
}

func (r *rig) stop() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	if err := <-r.done; err != nil {
		r.t.Errorf("Run returned %v, want nil after cancel", err)
	}
	r.cancel = nil
}

func (r *rig) enqueue(a actions.Action) actions.EnqueueResult {
	r.t.Helper()
	a.Bot = bot
	res, err := r.store.Enqueue(context.Background(), a)
	if err != nil {
		r.t.Fatalf("Enqueue %+v: %v", a, err)
	}
	r.ex.Wake()
	return res
}

func (r *rig) status(id int64) actions.Action {
	r.t.Helper()
	a, err := r.store.Get(context.Background(), id)
	if err != nil {
		r.t.Fatalf("Get %d: %v", id, err)
	}
	return a
}

func group(n int) wa.JID { return wa.JID(fmt.Sprintf("1203%04d@g.us", n)) }

func member(n int) wa.JID { return wa.JID(fmt.Sprintf("%d@lid", 5000+n)) }

func revoke(chat wa.JID, id string, sent time.Time, p actions.Priority) actions.Action {
	return actions.Action{
		Chat: chat, Kind: actions.KindRevoke, Priority: p,
		Payload:     actions.Payload{Target: wa.MessageRef{Sender: member(1), ID: wa.MessageID(id)}},
		DedupeKey:   actions.RevokeKey(chat, wa.MessageID(id)),
		MessageTime: sent,
	}
}

func kick(chat, who wa.JID) actions.Action {
	return actions.Action{
		Chat: chat, Kind: actions.KindKick, Priority: actions.P2,
		Payload: actions.Payload{JIDs: []wa.JID{who}}, DedupeKey: actions.KickKey(chat, who, "ban1"),
	}
}

func send(chat wa.JID, key, text string, purpose actions.Purpose, variants ...string) actions.Action {
	return actions.Action{
		Chat: chat, Kind: actions.KindSend, Priority: actions.P3, DedupeKey: key,
		Payload: actions.Payload{Text: text, Variants: variants, Purpose: purpose},
	}
}

// maxInWindow is the largest number of calls in any interval of length span.
func maxInWindow(calls []call, span time.Duration) int {
	best, j := 0, 0
	for i := range calls {
		for calls[i].at.Sub(calls[j].at) >= span {
			j++
		}
		best = max(best, i-j+1)
	}
	return best
}

// within runs f in a synctest bubble.
func within(t *testing.T, f func(t *testing.T)) {
	t.Helper()
	synctest.Test(t, f)
}
