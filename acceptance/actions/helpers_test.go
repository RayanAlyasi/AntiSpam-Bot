package actions_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"sort"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

const (
	botA wa.JID = "966500000001@s.whatsapp.net"
	botB wa.JID = "966500000002@s.whatsapp.net"

	spammer wa.JID = "111111@lid"
	eps            = 100 * time.Millisecond
)

func group(n int) wa.JID { return wa.JID(fmt.Sprintf("120363%09d@g.us", n)) }
func dm(n int) wa.JID    { return wa.JID(fmt.Sprintf("9665%08d@s.whatsapp.net", n)) }
func member(n int) wa.JID {
	return wa.JID(fmt.Sprintf("%06d@lid", 200000+n))
}

// ---- recording WAClient decorator ----

// call is one WhatsApp call as seen by the recorder, with virtual-time stamps.
type call struct {
	Method     string
	Chat       wa.JID
	Args       []any
	Start, End time.Time
	Err        error
}

var writeMethods = map[string]bool{
	fake.MethodSend: true, fake.MethodReact: true, fake.MethodRevoke: true, fake.MethodEdit: true,
	fake.MethodUpdateParticipants: true, fake.MethodUpdateJoinRequests: true,
	fake.MethodSetAnnounce: true, fake.MethodSetLocked: true, fake.MethodSetJoinApproval: true,
	fake.MethodResetInviteLink: true,
}

// recorder wraps a wa.WAClient, stamps every call with time.Now() (virtual inside a synctest
// bubble), tracks how many calls are in flight, and can add latency per method.
type recorder struct {
	inner wa.WAClient

	mu          sync.Mutex
	calls       []call
	inflight    int
	maxInflight int
	latency     map[string]time.Duration // "*" applies to every method
}

var _ wa.WAClient = (*recorder)(nil)

func newRecorder(inner wa.WAClient) *recorder {
	return &recorder{inner: inner, latency: map[string]time.Duration{}}
}

func (r *recorder) setLatency(method string, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.latency[method] = d
}

func (r *recorder) do(ctx context.Context, method string, chat wa.JID, args []any, f func() error) error {
	r.mu.Lock()
	r.inflight++
	r.maxInflight = max(r.maxInflight, r.inflight)
	lat, ok := r.latency[method]
	if !ok {
		lat = r.latency["*"]
	}
	r.mu.Unlock()
	start := time.Now()
	if lat > 0 {
		select {
		case <-time.After(lat):
		case <-ctx.Done():
		}
	}
	err := f()
	r.mu.Lock()
	r.inflight--
	r.calls = append(r.calls, call{Method: method, Chat: chat, Args: args, Start: start, End: time.Now(), Err: err})
	r.mu.Unlock()
	return err
}

// all returns every recorded call, ordered by start time.
func (r *recorder) all() []call {
	r.mu.Lock()
	out := slices.Clone(r.calls)
	r.mu.Unlock()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// writes returns the recorded write calls, ordered by start time.
func (r *recorder) writes() []call {
	var out []call
	for _, c := range r.all() {
		if writeMethods[c.Method] {
			out = append(out, c)
		}
	}
	return out
}

func (r *recorder) to(method string) []call {
	var out []call
	for _, c := range r.all() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (r *recorder) maxInFlight() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maxInflight
}

func (r *recorder) Send(ctx context.Context, chat wa.JID, msg wa.OutgoingMessage) (res wa.SendResult, err error) {
	err = r.do(ctx, fake.MethodSend, chat, []any{chat, msg}, func() error {
		res, err = r.inner.Send(ctx, chat, msg)
		return err
	})
	return res, err
}

func (r *recorder) React(ctx context.Context, chat wa.JID, target wa.MessageRef, emoji string) error {
	return r.do(ctx, fake.MethodReact, chat, []any{chat, target, emoji}, func() error {
		return r.inner.React(ctx, chat, target, emoji)
	})
}

func (r *recorder) Revoke(ctx context.Context, chat wa.JID, target wa.MessageRef) error {
	return r.do(ctx, fake.MethodRevoke, chat, []any{chat, target}, func() error {
		return r.inner.Revoke(ctx, chat, target)
	})
}

func (r *recorder) Edit(ctx context.Context, chat wa.JID, id wa.MessageID, text string) error {
	return r.do(ctx, fake.MethodEdit, chat, []any{chat, id, text}, func() error {
		return r.inner.Edit(ctx, chat, id, text)
	})
}

func (r *recorder) UpdateParticipants(ctx context.Context, g wa.JID, jids []wa.JID, action wa.ParticipantAction) (res []wa.ParticipantResult, err error) {
	err = r.do(ctx, fake.MethodUpdateParticipants, g, []any{g, slices.Clone(jids), action}, func() error {
		res, err = r.inner.UpdateParticipants(ctx, g, jids, action)
		return err
	})
	return res, err
}

func (r *recorder) JoinRequests(ctx context.Context, g wa.JID) (res []wa.JoinRequest, err error) {
	err = r.do(ctx, fake.MethodJoinRequests, g, []any{g}, func() error {
		res, err = r.inner.JoinRequests(ctx, g)
		return err
	})
	return res, err
}

func (r *recorder) UpdateJoinRequests(ctx context.Context, g wa.JID, jids []wa.JID, action wa.JoinRequestAction) (res []wa.ParticipantResult, err error) {
	err = r.do(ctx, fake.MethodUpdateJoinRequests, g, []any{g, slices.Clone(jids), action}, func() error {
		res, err = r.inner.UpdateJoinRequests(ctx, g, jids, action)
		return err
	})
	return res, err
}

func (r *recorder) SetAnnounce(ctx context.Context, g wa.JID, on bool) error {
	return r.do(ctx, fake.MethodSetAnnounce, g, []any{g, on}, func() error { return r.inner.SetAnnounce(ctx, g, on) })
}

func (r *recorder) SetLocked(ctx context.Context, g wa.JID, on bool) error {
	return r.do(ctx, fake.MethodSetLocked, g, []any{g, on}, func() error { return r.inner.SetLocked(ctx, g, on) })
}

func (r *recorder) SetJoinApproval(ctx context.Context, g wa.JID, on bool) error {
	return r.do(ctx, fake.MethodSetJoinApproval, g, []any{g, on}, func() error { return r.inner.SetJoinApproval(ctx, g, on) })
}

func (r *recorder) ResetInviteLink(ctx context.Context, g wa.JID) (link string, err error) {
	err = r.do(ctx, fake.MethodResetInviteLink, g, []any{g}, func() error {
		link, err = r.inner.ResetInviteLink(ctx, g)
		return err
	})
	return link, err
}

func (r *recorder) GroupInfo(ctx context.Context, g wa.JID) (res wa.GroupInfo, err error) {
	err = r.do(ctx, fake.MethodGroupInfo, g, []any{g}, func() error {
		res, err = r.inner.GroupInfo(ctx, g)
		return err
	})
	return res, err
}

func (r *recorder) JoinedGroups(ctx context.Context) (res []wa.GroupInfo, err error) {
	err = r.do(ctx, fake.MethodJoinedGroups, "", nil, func() error {
		res, err = r.inner.JoinedGroups(ctx)
		return err
	})
	return res, err
}

func (r *recorder) GroupInfoFromLink(ctx context.Context, code string) (res wa.GroupInfo, err error) {
	err = r.do(ctx, fake.MethodGroupInfoFromLink, "", []any{code}, func() error {
		res, err = r.inner.GroupInfoFromLink(ctx, code)
		return err
	})
	return res, err
}

func (r *recorder) UserInfo(ctx context.Context, jids []wa.JID) (res map[wa.JID]wa.UserInfo, err error) {
	err = r.do(ctx, fake.MethodUserInfo, "", []any{slices.Clone(jids)}, func() error {
		res, err = r.inner.UserInfo(ctx, jids)
		return err
	})
	return res, err
}

func (r *recorder) ProfilePicture(ctx context.Context, jid wa.JID, p wa.ProfilePictureParams) (res *wa.ProfilePicture, err error) {
	err = r.do(ctx, fake.MethodProfilePicture, jid, []any{jid, p}, func() error {
		res, err = r.inner.ProfilePicture(ctx, jid, p)
		return err
	})
	return res, err
}

func (r *recorder) NewsletterInfo(ctx context.Context, jid wa.JID) (res wa.NewsletterInfo, err error) {
	err = r.do(ctx, fake.MethodNewsletterInfo, jid, []any{jid}, func() error {
		res, err = r.inner.NewsletterInfo(ctx, jid)
		return err
	})
	return res, err
}

func (r *recorder) NewsletterInfoFromInvite(ctx context.Context, code string) (res wa.NewsletterInfo, err error) {
	err = r.do(ctx, fake.MethodNewsletterInfoFromInvite, "", []any{code}, func() error {
		res, err = r.inner.NewsletterInfoFromInvite(ctx, code)
		return err
	})
	return res, err
}

// ---- fake collaborators ----

type fakeGroups struct {
	mu       sync.Mutex
	posting  map[wa.JID]actions.Posting
	errs     map[wa.JID]error
	notAdmin []wa.JID
}

func newFakeGroups() *fakeGroups {
	return &fakeGroups{posting: map[wa.JID]actions.Posting{}, errs: map[wa.JID]error{}}
}

func (g *fakeGroups) set(chat wa.JID, p actions.Posting) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.posting[chat] = p
}

func (g *fakeGroups) fail(chat wa.JID, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.errs[chat] = err
}

func (g *fakeGroups) Posting(_ context.Context, chat wa.JID) (actions.Posting, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.errs[chat]; err != nil {
		return actions.Posting{}, err
	}
	return g.posting[chat], nil
}

func (g *fakeGroups) NotAdmin(_ context.Context, bot, grp wa.JID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.notAdmin = append(g.notAdmin, grp)
	return nil
}

func (g *fakeGroups) notAdminCalls() []wa.JID {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.notAdmin)
}

type fakeAlerts struct {
	mu     sync.Mutex
	alerts []actions.Alert
}

func (a *fakeAlerts) Alert(_ context.Context, al actions.Alert) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.alerts = append(a.alerts, al)
	return nil
}

func (a *fakeAlerts) count(kind actions.AlertKind) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, al := range a.alerts {
		if al.Kind == kind {
			n++
		}
	}
	return n
}

type fakeAudit struct {
	mu      sync.Mutex
	entries []actions.AuditEntry
}

func (a *fakeAudit) Audit(_ context.Context, e actions.AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

func (a *fakeAudit) has(action string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.entries {
		if e.Action == action {
			return true
		}
	}
	return false
}

// ---- harness ----

// harness is one bot's executor on a MemStore, a fake WhatsApp client and fake collaborators.
// It must be created inside a synctest bubble.
type harness struct {
	t      *testing.T
	cfg    actions.Config
	bot    wa.JID
	store  *actions.MemStore
	wa     *fake.Client
	rec    *recorder
	groups *fakeGroups
	alerts *fakeAlerts
	audit  *fakeAudit
	op     actions.Operator
	exec   *actions.Executor
	t0     time.Time

	runMu   sync.Mutex
	started bool
	runDone chan struct{}
	runErr  error
}

func newHarness(t *testing.T, cfg actions.Config) *harness {
	t.Helper()
	return newHarnessOn(t, cfg, actions.NewMemStore(clock.Real{}), botA, 1)
}

func newHarnessOn(t *testing.T, cfg actions.Config, store *actions.MemStore, bot wa.JID, seed uint64) *harness {
	t.Helper()
	h := &harness{
		t: t, cfg: cfg, bot: bot, store: store, wa: fake.New(),
		groups: newFakeGroups(), alerts: &fakeAlerts{}, audit: &fakeAudit{}, t0: time.Now(),
	}
	h.rec = newRecorder(h.wa)
	h.op = actions.Operator{Store: store, Audit: h.audit, Clock: clock.Real{}}
	exec, err := actions.NewExecutor(cfg, h.deps(seed))
	if err != nil {
		t.Fatalf("NewExecutor: %v, want nil for a valid config and deps", err)
	}
	h.exec = exec
	return h
}

func (h *harness) deps(seed uint64) actions.Deps {
	return actions.Deps{
		Bot: h.bot, Client: h.rec, Store: h.store, Clock: clock.Real{},
		Rand: rand.New(rand.NewPCG(seed, 2)), Groups: h.groups, Alerts: h.alerts, Log: zerolog.Nop(),
	}
}

// start runs the executor until the test ends.
func (h *harness) start() {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h.runMu.Lock()
	h.started = true
	h.runDone = make(chan struct{})
	h.runMu.Unlock()
	go func() {
		err := h.exec.Run(ctx)
		h.runMu.Lock()
		h.runErr = err
		h.runMu.Unlock()
		close(h.runDone)
	}()
	h.t.Cleanup(func() {
		cancel()
		<-h.runDone
		if h.runErr != nil {
			h.t.Errorf("Run returned %v after its context was cancelled, want nil (executor contract, DESIGN §10.1)", h.runErr)
		}
	})
}

// checkRunning fails the test if Run has returned before the test cancelled it.
func (h *harness) checkRunning() {
	h.t.Helper()
	select {
	case <-h.runDone:
		h.t.Fatalf("Run returned early with %v, want it to keep running until cancelled (DESIGN §10.1)", h.runErr)
	default:
	}
}

// advance lets d of virtual time pass, then waits for the executor to settle.
func (h *harness) advance(d time.Duration) {
	time.Sleep(d)
	synctest.Wait()
}

func (h *harness) enqueue(a actions.Action) actions.EnqueueResult {
	h.t.Helper()
	res, err := h.store.Enqueue(context.Background(), a)
	if err != nil {
		h.t.Fatalf("Enqueue(%s %s): %v, want nil", a.Kind, a.DedupeKey, err)
	}
	h.runMu.Lock()
	started := h.started
	h.runMu.Unlock()
	if started {
		h.exec.Wake()
	}
	return res
}

// add enqueues a and fails unless it was inserted.
func (h *harness) add(a actions.Action) int64 {
	h.t.Helper()
	res := h.enqueue(a)
	if !res.Inserted {
		h.t.Fatalf("Enqueue(%s %s) = %+v, want Inserted", a.Kind, a.DedupeKey, res)
	}
	return res.ID
}

func (h *harness) get(id int64) actions.Action {
	h.t.Helper()
	a, err := h.store.Get(context.Background(), id)
	if err != nil {
		h.t.Fatalf("Get(%d): %v", id, err)
	}
	return a
}

// waitFinal advances time in steps of a second until every stored action is final, or fails
// after limit.
func (h *harness) waitFinal(limit time.Duration, why string) {
	h.t.Helper()
	deadline := time.Now().Add(limit)
	for {
		var live []string
		for _, a := range h.store.All() {
			if !a.Status.Final() {
				live = append(live, fmt.Sprintf("#%d %s %s %s", a.ID, a.Priority, a.Kind, a.Status))
			}
		}
		if len(live) == 0 {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("after %s, %d actions still live (first: %v); want all final: %s",
				limit, len(live), live[:min(5, len(live))], why)
		}
		h.checkRunning()
		h.advance(time.Second)
	}
}

func (h *harness) state() actions.State { return h.exec.State() }

func (h *harness) off(t time.Time) time.Duration { return t.Sub(h.t0) }

// ---- action builders ----

func revoke(chat wa.JID, id string, p actions.Priority, age time.Duration) actions.Action {
	mid := wa.MessageID(id)
	return actions.Action{
		Bot: botA, Chat: chat, Kind: actions.KindRevoke, Priority: p,
		Payload:     actions.Payload{Target: wa.MessageRef{Sender: spammer, ID: mid}},
		DedupeKey:   actions.RevokeKey(chat, mid),
		MessageTime: time.Now().Add(-age),
	}
}

func kick(g wa.JID, cause string, jids ...wa.JID) actions.Action {
	return actions.Action{
		Bot: botA, Chat: g, Kind: actions.KindKick, Priority: actions.P2,
		Payload:   actions.Payload{JIDs: jids},
		DedupeKey: actions.KickKey(g, jids[0], cause),
	}
}

func rejectJoin(g wa.JID, cause string, jids ...wa.JID) actions.Action {
	return actions.Action{
		Bot: botA, Chat: g, Kind: actions.KindRejectJoin, Priority: actions.P2,
		Payload:   actions.Payload{JIDs: jids},
		DedupeKey: actions.RejectJoinKey(g, jids[0], cause),
	}
}

func send(chat wa.JID, key, text string, variants ...string) actions.Action {
	return actions.Action{
		Bot: botA, Chat: chat, Kind: actions.KindSend, Priority: actions.P3,
		Payload:   actions.Payload{Text: text, Variants: variants, Purpose: actions.PurposeDM},
		DedupeKey: "send:" + key,
	}
}

func lockdown(g wa.JID, kind actions.Kind, key string) actions.Action {
	return actions.Action{
		Bot: botA, Chat: g, Kind: kind, Priority: actions.P0,
		Payload:   actions.Payload{On: true},
		DedupeKey: string(kind) + ":" + string(g) + ":" + key,
	}
}

// post builds a valid action of a posting kind (send, edit, react) in chat.
func post(kind actions.Kind, chat wa.JID, p actions.Priority, key string) actions.Action {
	a := actions.Action{Bot: botA, Chat: chat, Kind: kind, Priority: p, DedupeKey: string(kind) + ":" + key}
	switch kind {
	case actions.KindSend:
		a.Payload = actions.Payload{Text: "notice " + key, Purpose: actions.PurposeNotice}
	case actions.KindEdit:
		a.Payload = actions.Payload{Target: wa.MessageRef{ID: wa.MessageID("BOT" + key)}, Text: "edited " + key}
	case actions.KindReact:
		a.Payload = actions.Payload{Target: wa.MessageRef{Sender: spammer, ID: wa.MessageID("M" + key)}, Emoji: "✅"}
	default:
		panic("post: not a posting kind: " + string(kind))
	}
	return a
}

// withBot returns a with its bot replaced.
func withBot(a actions.Action, bot wa.JID) actions.Action {
	a.Bot = bot
	return a
}

// setGroup registers g in the fake with members 1..n, so lockdowns and kicks there succeed.
func (h *harness) setGroup(g wa.JID, n int) {
	ps := []wa.Participant{{JID: "999999@lid", IsAdmin: true, IsSuperAdmin: true}}
	for i := 1; i <= n; i++ {
		ps = append(ps, wa.Participant{JID: member(i)})
	}
	h.wa.SetGroup(wa.GroupInfo{JID: g, Name: string(g), Participants: ps, Size: len(ps)})
}

// ---- checks ----

func starts(cs []call) []time.Time {
	out := make([]time.Time, len(cs))
	for i, c := range cs {
		out[i] = c.Start
	}
	return out
}

// checkBucket fails if more than burst + perMin·W calls start in any interval of length W.
func checkBucket(t *testing.T, what string, ts []time.Time, burst int, perMin float64, why string) {
	t.Helper()
	for i := range ts {
		for j := i; j < len(ts); j++ {
			n := j - i + 1
			allowed := float64(burst) + perMin*ts[j].Sub(ts[i]).Minutes()
			if float64(n) > allowed+1e-9 {
				t.Fatalf("%s: %d calls in %s (calls #%d..#%d), want at most %.2f (burst %d + %g/min): %s",
					what, n, ts[j].Sub(ts[i]), i, j, allowed, burst, perMin, why)
			}
		}
	}
}

// checkPerChat fails if any chat gets more than perMin writes in any 60 s.
func checkPerChat(t *testing.T, writes []call, perMin int, why string) {
	t.Helper()
	byChat := map[wa.JID][]time.Time{}
	for _, c := range writes {
		byChat[c.Chat] = append(byChat[c.Chat], c.Start)
	}
	for chat, ts := range byChat {
		for i := 0; i+perMin < len(ts); i++ {
			if d := ts[i+perMin].Sub(ts[i]); d < time.Minute {
				t.Fatalf("chat %s got %d writes within %s, want at most %d in any 60 s: %s", chat, perMin+1, d, perMin, why)
			}
		}
	}
}

func revokedIDs(cs []call) []wa.MessageID {
	var out []wa.MessageID
	for _, c := range cs {
		if c.Method == fake.MethodRevoke && c.Err == nil {
			out = append(out, c.Args[1].(wa.MessageRef).ID)
		}
	}
	return out
}

func approxEq(a, b float64) bool { return a-b < 1e-6 && b-a < 1e-6 }
