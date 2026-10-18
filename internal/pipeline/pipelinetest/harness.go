//go:build integration

package pipelinetest

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/neardup"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	reviewpg "github.com/RayanAlyasi/AntiSpam-Bot/internal/review/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter/synth"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// Preset is a group's settings preset (DESIGN.md §12.2).
type Preset string

// Presets.
const (
	PresetRelaxed   Preset = "relaxed"
	PresetStandard  Preset = "standard"
	PresetStrict    Preset = "strict"
	PresetAskAdmins Preset = "ask_admins"
)

// Enforcement is enforcement.mode (DESIGN.md §8.3). Empty means the preset's own mode.
type Enforcement string

// Enforcement modes.
const (
	EnforcementPreset          Enforcement = ""
	EnforcementAuto            Enforcement = "auto"
	EnforcementAskDeleteFirst  Enforcement = "ask_delete_first"
	EnforcementAskBeforeAction Enforcement = "ask_before_action"
)

// Settings are the group settings a test controls. Zero values mean enforce, standard and the
// preset's enforcement mode.
type Settings struct {
	Mode        pipeline.Mode
	Preset      Preset
	Enforcement Enforcement
	ReviewGroup wa.JID
}

// Member is a person in a group: LID always, phone number when known.
type Member struct {
	LID wa.JID
	PN  wa.JID
}

// Group is a protected group as the bot finds it at onboarding.
type Group struct {
	JID      wa.JID
	Admins   []Member
	Members  []Member // pre-existing at onboarding, so established (DESIGN.md §8.1)
	Settings Settings
}

// Message is a whatsmeow-free description of an incoming group message.
type Message struct {
	Group    wa.JID
	Sender   wa.JID // LID
	SenderPN wa.JID // optional
	ID       wa.MessageID
	Time     time.Time // zero means the fake clock's now
	Text     string
	VCards   []string // raw vCard documents shared as contacts
}

// Options configure New. Zero values get defaults.
type Options struct {
	Start time.Time
	Bot   wa.JID
}

// maxDrain is the most fake time one Deliver lets pass while the queue drains: enough for
// pacing and kick coalescing, far below any review timeout.
const maxDrain = 60 * time.Second

type member struct {
	rec      policy.MemberRecord
	personID int64
}

type group struct {
	settings policy.Settings
	admins   map[wa.JID]bool
	members  map[wa.JID]*member
	index    *neardup.Index
	entries  int
}

// Harness is the bot under test.
type Harness struct {
	t      testing.TB
	ctx    context.Context
	clk    *clock.Fake
	wa     *fake.Client
	bot    wa.JID
	pool   *pgxpool.Pool
	queue  *actions.PGStore
	exec   *actions.Executor
	pipe   *pipeline.Pipeline
	policy *store.PolicyStore

	mu       sync.Mutex
	groups   map[wa.JID]*group
	persons  map[wa.JID]int64 // LID or PN → person ID
	verdicts map[string]pipeline.Verdict
	analyzer []pipeline.Analyzer
	rules    *rules.Holder
	hash     func(e164 string) string
}

// New builds the bot on a fresh, isolated database. It fails t if Postgres can't start.
func New(t testing.TB, opts Options) *Harness {
	t.Helper()
	if opts.Start.IsZero() {
		opts.Start = time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	}
	if opts.Bot == "" {
		opts.Bot = "100000000000001@lid"
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &Harness{
		t: t, ctx: ctx, clk: clock.NewFake(opts.Start), wa: fake.New(), bot: opts.Bot,
		groups: map[wa.JID]*group{}, persons: map[wa.JID]int64{}, verdicts: map[string]pipeline.Verdict{},
	}
	h.pool = storetest.Pool(t)
	h.policy = store.NewPolicyStore(h.pool)
	h.queue = actions.NewPGStore(h.pool, h.clk)

	exec, err := actions.NewExecutor(actions.DefaultConfig(), actions.Deps{
		Bot: h.bot, Client: h.wa, Store: h.queue, Clock: h.clk, Groups: postingFunc(h.posting), Log: zerolog.New(io.Discard),
	})
	if err != nil {
		t.Fatalf("pipelinetest: executor: %v", err)
	}
	h.exec = exec
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Any error after cleanup's cancel is the shutdown: pgx reports a write cut short by the
		// cancel as a plain i/o timeout, not context.Canceled.
		if err := exec.Run(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("pipelinetest: executor stopped: %v", err)
		}
	}()
	t.Cleanup(func() { cancel(); <-done })

	hasher, err := blacklist.NewHasher([]byte("pipelinetest-number-hash-key-0001"))
	if err != nil {
		t.Fatalf("pipelinetest: hasher: %v", err)
	}
	h.hash = hasher.Hash
	h.rules = rules.NewHolder(store.RulesLoader{Q: h.pool}, h.clk, zerolog.New(io.Discard))
	if err := h.rules.Reload(ctx); err != nil {
		t.Fatalf("pipelinetest: load rules: %v", err)
	}
	h.analyzer = pipeline.AnalyzerSet{Rules: h.rules, Hash: h.hash, Groups: groupsFunc(h.settingsOf), Bot: h.bot, Corpus: h.indexes}.Analyzers()
	engine, err := pipeline.NewEngine(pipeline.EngineConfig{
		Analyzers: h.analyzer, Weights: scoring.DefaultWeights(), Normalize: pipeline.NormalizeTexts, Clock: h.clk,
	})
	if err != nil {
		t.Fatalf("pipelinetest: engine: %v", err)
	}
	// One review service: its store and queued posts write through the message's transaction
	// when a detection opens an item (pgstore.Conn, pgstore.TxFrom), and through the pool
	// otherwise.
	reviews := review.NewService(review.Deps{
		Store:    reviewpg.Reviews{DB: pgstore.Conn{Pool: h.pool}},
		Settings: reviewSettings(h.reviewConfig),
		Admins:   adminsFunc(h.isAdmin),
		Actions: pipeline.ReviewActions{Bot: h.bot, Queue: func(ctx context.Context, a actions.Action) error {
			if tx, ok := pgstore.TxFrom(ctx); ok {
				_, err := h.queue.EnqueueTx(ctx, tx, a)
				return err
			}
			_, err := h.queue.Enqueue(ctx, a)
			return err
		}},
		Learner:   noLearning{},
		Clock:     h.clk,
		Protected: func(_ context.Context, chat wa.JID) (bool, error) { return h.protected(chat), nil },
	})
	enqueueTx := func(ctx context.Context, tx db.DBTX, a actions.Action) error {
		_, err := h.queue.EnqueueTx(ctx, tx, a)
		return err
	}
	h.pipe, err = pipeline.New(pipeline.Config{
		Engine: engine, Groups: groupsFunc(h.settingsOf), Members: membersFunc(h.sender),
		Store: pgstore.New(h.pool, enqueueTx, reviews),
		Bot:   h.bot, Clock: h.clk, RuleVersion: scoring.DefaultWeights().Version, Committed: exec.Wake,
	})
	if err != nil {
		t.Fatalf("pipelinetest: pipeline: %v", err)
	}
	return h
}

// WA returns the fake WhatsApp client.
func (h *Harness) WA() *fake.Client { return h.wa }

// Clock returns the fake clock.
func (h *Harness) Clock() *clock.Fake { return h.clk }

// Bot returns the bot's own JID.
func (h *Harness) Bot() wa.JID { return h.bot }

// AddGroup onboards a protected group, with the bot as an admin.
func (h *Harness) AddGroup(g Group) {
	h.t.Helper()
	s, err := policy.PresetSettings(policy.Preset(cmpOr(string(g.Settings.Preset), string(PresetStandard))))
	if err != nil {
		h.t.Fatalf("pipelinetest: preset %q: %v", g.Settings.Preset, err)
	}
	s.Mode = policy.Mode(cmpOr(string(g.Settings.Mode), string(pipeline.ModeEnforce)))
	if g.Settings.Enforcement != EnforcementPreset {
		s.Enforcement.Mode = policy.EnforcementMode(g.Settings.Enforcement)
	}
	if g.Settings.ReviewGroup != "" {
		s.Review.Destination, s.Review.ReviewGroup = "group", string(g.Settings.ReviewGroup)
	}
	if err := s.Validate(); err != nil {
		h.t.Fatalf("pipelinetest: settings of %s: %v", g.JID, err)
	}
	gs := &group{settings: s, admins: map[wa.JID]bool{h.bot: true}, members: map[wa.JID]*member{}, index: neardup.NewIndex(neardup.DefaultThresholds())}
	joined := h.clk.Now().AddDate(-1, 0, 0)
	info := wa.GroupInfo{JID: g.JID, Name: string(g.JID), Participants: []wa.Participant{{JID: h.bot, LID: h.bot, IsAdmin: true}}}
	h.mu.Lock()
	for _, m := range g.Admins {
		gs.admins[m.LID] = true
		gs.members[m.LID] = h.newMember(m, policy.RoleAdmin, joined, true)
		info.Participants = append(info.Participants, wa.Participant{JID: m.LID, LID: m.LID, PhoneNumber: m.PN, IsAdmin: true})
	}
	for _, m := range g.Members {
		gs.members[m.LID] = h.newMember(m, policy.RoleMember, joined, true)
		info.Participants = append(info.Participants, wa.Participant{JID: m.LID, LID: m.LID, PhoneNumber: m.PN})
	}
	h.groups[g.JID] = gs
	h.mu.Unlock()
	info.Size = len(info.Participants)
	h.wa.SetGroup(info)
}

// Join records that m joined group now, which makes m a new member.
func (h *Harness) Join(groupJID wa.JID, m Member) {
	h.t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	g := h.group(groupJID)
	g.members[m.LID] = h.newMember(m, policy.RoleMember, h.clk.Now(), false)
}

// ConfirmSpam stores text as admin-confirmed spam in group's corpus (DESIGN.md §20 v0.1).
func (h *Harness) ConfirmSpam(groupJID wa.JID, text string) {
	h.t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	g := h.group(groupJID)
	g.entries++
	g.index.Add(fmt.Sprintf("%s#%d", groupJID, g.entries), neardup.FingerprintText(text))
}

// Overturn records an admin "not spam" decision on text in group (DESIGN.md §7.5): any corpus
// entry it matches is retracted, and its fingerprint is allow-listed.
func (h *Harness) Overturn(groupJID wa.JID, text string) {
	h.t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	g := h.group(groupJID)
	fp := neardup.FingerprintText(text)
	for range g.entries + 1 {
		m := g.index.Match(fp)
		if m.Closeness != neardup.Certain {
			break
		}
		g.index.Remove(m.EntryID)
	}
	g.index.Allow(fp)
}

// Blacklist adds a phone number to group's manual blacklist (DESIGN.md §9.8).
func (h *Harness) Blacklist(groupJID wa.JID, number string) {
	h.t.Helper()
	e164, err := normalize.ParsePhone(number, "")
	if err != nil {
		h.t.Fatalf("pipelinetest: blacklist %q: %v", number, err)
	}
	if _, _, err := store.AddBlacklist(h.ctx, h.pool, store.BlacklistAdd{Scope: rules.BlacklistGroup, GroupJID: string(groupJID),
		E164: e164, Hash: h.hash(e164), Reason: "test", Now: h.clk.Now()}); err != nil {
		h.t.Fatalf("pipelinetest: blacklist %s: %v", e164, err)
	}
	// Production reloads on the rules_changed NOTIFY; the harness reloads at once.
	if err := h.rules.Reload(h.ctx); err != nil {
		h.t.Fatalf("pipelinetest: reload rules: %v", err)
	}
}

// Deliver runs msg through the bot, drains the due actions and returns the verdict.
func (h *Harness) Deliver(msg Message) pipeline.Verdict {
	h.t.Helper()
	at := msg.Time
	if at.IsZero() {
		at = h.clk.Now()
	}
	env, err := synth.Envelope(synth.Spec{
		Chat: msg.Group, Sender: msg.Sender, SenderPN: msg.SenderPN, ID: msg.ID, Time: at,
		Text: msg.Text, VCards: msg.VCards,
	}, h.bot)
	if err != nil {
		h.t.Fatalf("pipelinetest: build %s: %v", msg.ID, err)
	}
	v, err := h.pipe.Process(h.ctx, env, at)
	if err != nil {
		h.t.Fatalf("pipelinetest: process %s: %v", msg.ID, err)
	}
	h.drain()
	h.mu.Lock()
	if !v.Duplicate {
		h.verdicts[key(msg.Group, msg.ID)] = v
	}
	h.mu.Unlock()
	return v
}

// Detection returns the stored detection for a message, if one was recorded.
func (h *Harness) Detection(groupJID wa.JID, id wa.MessageID) (pipeline.Verdict, bool) {
	h.t.Helper()
	rows, err := h.policy.ListDetections(h.ctx, groupJID, 0, 1000)
	if err != nil {
		h.t.Fatalf("pipelinetest: detections of %s: %v", groupJID, err)
	}
	for _, r := range rows {
		if r.MsgID != string(id) {
			continue
		}
		v := pipeline.Verdict{
			Group: groupJID, Message: id, Mode: pipeline.Mode(r.Mode), Route: pipeline.Route(r.Route),
			Source: pipeline.Source(r.ConfidenceSource.String),
		}
		h.mu.Lock()
		if seen, ok := h.verdicts[key(groupJID, id)]; ok {
			v.Sender, v.Band, v.Explanation = seen.Sender, seen.Band, seen.Explanation
		}
		h.mu.Unlock()
		if v.Mode == pipeline.Mode(policy.DetectionEnforce) {
			v.Mode = pipeline.ModeEnforce
		}
		v.Actions = planKinds(r.ActionPlan)
		return v, true
	}
	return pipeline.Verdict{}, false
}

// Banned reports whether member has an active ban (rejoin block) in group.
func (h *Harness) Banned(groupJID, memberJID wa.JID) bool {
	h.t.Helper()
	h.mu.Lock()
	pid, ok := h.persons[memberJID]
	h.mu.Unlock()
	if !ok {
		return false
	}
	b, ok, err := h.policy.ActiveBan(h.ctx, groupJID, pid)
	if err != nil {
		h.t.Fatalf("pipelinetest: active ban: %v", err)
	}
	return ok && b.Active && b.RejoinBlock
}

// drain lets the executor run every due action, advancing the fake clock only while the
// executor waits, by at most maxDrain in all.
func (h *Harness) drain() {
	deadline := h.clk.Now().Add(maxDrain)
	for idle := 0; idle < 200; {
		if !h.busy() {
			// Settle: a just-finished kick may have woken more work.
			idle++
			if idle > 3 {
				return
			}
			waitBriefly()
			continue
		}
		idle = 0
		if h.clk.Waiters() > 0 && h.clk.Now().Before(deadline) {
			h.clk.Advance(min(time.Second, deadline.Sub(h.clk.Now())))
			continue
		}
		if !h.clk.Now().Before(deadline) {
			return
		}
		waitBriefly()
	}
}

// busy reports whether the queue holds actions that can still run in the drain window.
func (h *Harness) busy() bool {
	n, err := db.New(h.pool).CountOpenActions(h.ctx, db.CountOpenActionsParams{Bot: string(h.bot), DueBefore: h.clk.Now().Add(maxDrain)})
	if err != nil {
		h.t.Fatalf("pipelinetest: count open actions: %v", err)
	}
	return n > 0
}

func (h *Harness) newMember(m Member, role policy.Role, joined time.Time, preExisting bool) *member {
	pid, ok := h.persons[m.LID]
	if !ok {
		pid = int64(len(h.persons) + 1)
		h.persons[m.LID] = pid
		if m.PN != "" {
			h.persons[m.PN] = pid
		}
	}
	return &member{rec: policy.MemberRecord{LID: string(m.LID), Role: role, JoinedAt: joined, PreExisting: preExisting}, personID: pid}
}

func (h *Harness) group(j wa.JID) *group {
	g, ok := h.groups[j]
	if !ok {
		h.t.Fatalf("pipelinetest: group %s wasn't added", j)
	}
	return g
}

func (h *Harness) indexes(j wa.JID) []*neardup.Index {
	h.mu.Lock()
	defer h.mu.Unlock()
	if g, ok := h.groups[j]; ok {
		return []*neardup.Index{g.index}
	}
	return nil
}

func (h *Harness) settingsOf(_ context.Context, j wa.JID) (policy.Settings, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	g, ok := h.groups[j]
	if !ok {
		return policy.Settings{}, false, nil
	}
	return g.settings.Clone(), true, nil
}

func (h *Harness) sender(_ context.Context, j wa.JID, who envelope.Identity) (pipeline.Sender, int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if g, ok := h.groups[j]; ok {
		if m, ok := g.members[who.LID]; ok {
			return pipeline.Sender{Member: m.rec, Known: true}, m.personID, nil
		}
	}
	// Unknown sender: a person record is created, as identity capture would (DESIGN.md §7.7).
	pid, ok := h.persons[who.LID]
	if !ok {
		pid = int64(len(h.persons) + 1)
		h.persons[who.LID] = pid
	}
	if who.PN != "" {
		h.persons[who.PN] = pid
	}
	return pipeline.Sender{}, pid, nil
}

func (h *Harness) protected(chat wa.JID) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.groups[chat]
	return ok
}

func (h *Harness) posting(_ context.Context, chat wa.JID) (actions.Posting, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if g, ok := h.groups[chat]; ok {
		return actions.Posting{Protected: true, Silent: g.settings.Silent}, nil
	}
	return actions.Posting{}, nil
}

func (h *Harness) reviewConfig(_ context.Context, j wa.JID) (review.Config, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	g, ok := h.groups[j]
	if !ok {
		return review.Config{}, fmt.Errorf("group %s isn't protected", j)
	}
	return review.ConfigFrom(g.settings), nil
}

func (h *Harness) isAdmin(_ context.Context, j, person wa.JID) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	g, ok := h.groups[j]
	return ok && g.admins[person], nil
}

func key(g wa.JID, id wa.MessageID) string { return string(g) + "/" + string(id) }

func planKinds(raw []byte) []pipeline.ActionKind {
	var plan []pgstore.PlannedAction
	if err := jsonUnmarshal(raw, &plan); err != nil {
		return nil
	}
	var out []pipeline.ActionKind
	for _, p := range plan {
		if k := pipeline.ActionKind(p.Kind); !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

type postingFunc func(context.Context, wa.JID) (actions.Posting, error)

func (f postingFunc) Posting(ctx context.Context, chat wa.JID) (actions.Posting, error) {
	return f(ctx, chat)
}

func (postingFunc) NotAdmin(context.Context, wa.JID, wa.JID) error { return nil }

type groupsFunc func(context.Context, wa.JID) (policy.Settings, bool, error)

func (f groupsFunc) Settings(ctx context.Context, g wa.JID) (policy.Settings, bool, error) {
	return f(ctx, g)
}

type membersFunc func(context.Context, wa.JID, envelope.Identity) (pipeline.Sender, int64, error)

func (f membersFunc) Sender(ctx context.Context, g wa.JID, who envelope.Identity) (pipeline.Sender, int64, error) {
	return f(ctx, g, who)
}

type reviewSettings func(context.Context, wa.JID) (review.Config, error)

func (f reviewSettings) ReviewConfig(ctx context.Context, g wa.JID) (review.Config, error) {
	return f(ctx, g)
}

type adminsFunc func(context.Context, wa.JID, wa.JID) (bool, error)

func (f adminsFunc) IsAdmin(ctx context.Context, g, p wa.JID) (bool, error) { return f(ctx, g, p) }

// noLearning ignores review decisions: deciding reviews isn't part of the fast lane.
type noLearning struct{}

func (noLearning) ConfirmSpam(context.Context, review.Confirmation) error { return nil }
func (noLearning) Overturn(context.Context, review.Overturn) error        { return nil }
