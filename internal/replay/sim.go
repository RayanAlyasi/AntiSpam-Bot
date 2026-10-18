package replay

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/neardup"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter/synth"
)

// simHashKey keys the simulator's number hashes. It only ever hashes fixture numbers in memory.
var simHashKey = []byte("antispam-replay-simulator-key-01")

// SimOptions configure NewSim. Zero values get defaults.
type SimOptions struct {
	Start   time.Time       // the fake clock's start; default 2026-01-01 UTC
	Bot     wa.JID          // the bot's JID
	Weights scoring.Weights // the rule/weight version under test; default scoring.DefaultWeights
	// Analyzers are extra analyzers to run besides the v0.1 set (rules, blacklist, corpus).
	Analyzers []pipeline.Analyzer
}

// Sim is an in-memory bot for replays: the real envelope builder, normalizer, analyzers,
// scoring and routing, with group state, the corpus and the store held in memory. Nothing is
// sent anywhere; reviews are only counted. It implements Target.
type Sim struct {
	clk  *clock.Fake
	bot  wa.JID
	pipe *pipeline.Pipeline

	rules *rules.Holder
	hash  func(e164 string) string

	mu        sync.Mutex
	groups    map[wa.JID]*simGroup
	persons   map[wa.JID]int64
	reviews   int
	blacklist []rules.BlacklistEntry
}

type simGroup struct {
	settings policy.Settings
	members  map[wa.JID]policy.MemberRecord
	index    *neardup.Index
	entries  int
}

// NewSim builds a simulator.
func NewSim(o SimOptions) (*Sim, error) {
	if o.Start.IsZero() {
		o.Start = time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	}
	if o.Bot == "" {
		o.Bot = "100000000000001@lid"
	}
	if o.Weights.Version == "" {
		o.Weights = scoring.DefaultWeights()
	}
	s := &Sim{clk: clock.NewFake(o.Start), bot: o.Bot, groups: map[wa.JID]*simGroup{}, persons: map[wa.JID]int64{}}
	hasher, err := blacklist.NewHasher(simHashKey)
	if err != nil {
		return nil, err
	}
	s.hash = hasher.Hash
	s.rules = rules.NewHolder(simLoader{s}, s.clk, zerolog.Nop())
	if err := s.rules.Reload(context.Background()); err != nil {
		return nil, err
	}
	set := pipeline.AnalyzerSet{Rules: s.rules, Hash: s.hash, Groups: simGroups{s}, Bot: o.Bot, Corpus: s.indexes}
	an := append(set.Analyzers(), o.Analyzers...)
	en, err := pipeline.NewEngine(pipeline.EngineConfig{Analyzers: an, Weights: o.Weights, Normalize: pipeline.NormalizeTexts, Clock: s.clk})
	if err != nil {
		return nil, err
	}
	st := pipeline.NewMemStore()
	st.Reviews = simReviews{s}
	s.pipe, err = pipeline.New(pipeline.Config{
		Engine: en, Groups: simGroups{s}, Members: simMembers{s}, Store: st,
		Bot: o.Bot, Clock: s.clk, RuleVersion: o.Weights.Version,
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Reviews returns how many review items the replay opened.
func (s *Sim) Reviews() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reviews
}

// AddGroup implements Target.
func (s *Sim) AddGroup(r Record) error {
	preset := policy.Preset(r.Preset)
	if preset == "" {
		preset = policy.PresetStandard
	}
	st, err := policy.PresetSettings(preset)
	if err != nil {
		return err
	}
	if r.Mode != "" {
		st.Mode = policy.Mode(r.Mode)
	}
	if r.Enforcement != "" {
		st.Enforcement.Mode = policy.EnforcementMode(r.Enforcement)
	}
	if r.ReviewGroup != "" {
		st.Review.Destination, st.Review.ReviewGroup = "group", string(r.ReviewGroup)
	}
	if err := st.Validate(); err != nil {
		return fmt.Errorf("settings of %s: %w", r.Group, err)
	}
	g := &simGroup{settings: st, members: map[wa.JID]policy.MemberRecord{}, index: neardup.NewIndex(neardup.DefaultThresholds())}
	long := s.clk.Now().AddDate(-1, 0, 0)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range r.Admins {
		g.members[m.LID] = policy.MemberRecord{LID: string(m.LID), Role: policy.RoleAdmin, JoinedAt: long, PreExisting: true}
		s.person(m)
	}
	for _, m := range r.Members {
		g.members[m.LID] = policy.MemberRecord{LID: string(m.LID), Role: policy.RoleMember, JoinedAt: long, PreExisting: true}
		s.person(m)
	}
	s.groups[r.Group] = g
	return nil
}

// Join implements Target.
func (s *Sim) Join(group wa.JID, m Member) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.groups[group]
	if !ok {
		return fmt.Errorf("group %s wasn't added", group)
	}
	g.members[m.LID] = policy.MemberRecord{LID: string(m.LID), Role: policy.RoleMember, JoinedAt: s.clk.Now()}
	s.person(m)
	return nil
}

// ConfirmSpam implements Target.
func (s *Sim) ConfirmSpam(group wa.JID, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.groups[group]
	if !ok {
		return fmt.Errorf("group %s wasn't added", group)
	}
	g.entries++
	g.index.Add(fmt.Sprintf("%s#%d", group, g.entries), neardup.FingerprintText(text))
	return nil
}

// Blacklist implements Target.
func (s *Sim) Blacklist(group wa.JID, number string) error {
	e164, err := normalize.ParsePhone(number, "")
	if err != nil {
		return fmt.Errorf("blacklist %q: %w", number, err)
	}
	s.mu.Lock()
	s.blacklist = append(s.blacklist, rules.BlacklistEntry{ID: int64(len(s.blacklist) + 1), Scope: rules.BlacklistGroup, GroupJID: string(group), PNHash: s.hash(e164)})
	s.mu.Unlock()
	return s.rules.Reload(context.Background())
}

// simLoader serves the simulator's blacklist to the rules holder; fixtures add no other rules.
type simLoader struct{ s *Sim }

func (l simLoader) LoadRules(context.Context) ([]rules.Rule, error) { return nil, nil }

func (l simLoader) LoadFingerprints(context.Context) ([]rules.Fingerprint, error) { return nil, nil }

func (l simLoader) LoadBlacklist(context.Context) ([]rules.BlacklistEntry, error) {
	l.s.mu.Lock()
	defer l.s.mu.Unlock()
	return append([]rules.BlacklistEntry(nil), l.s.blacklist...), nil
}

// Advance implements Target.
func (s *Sim) Advance(d time.Duration) error {
	s.clk.Advance(d)
	return nil
}

// Deliver implements Target.
func (s *Sim) Deliver(r Record) (pipeline.Verdict, error) {
	at := r.Time
	if at.IsZero() {
		at = s.clk.Now()
	}
	e, err := synth.Envelope(synth.Spec{Chat: r.Group, Sender: r.Sender, SenderPN: r.SenderPN, ID: r.ID, Time: at, Text: r.Text, VCards: r.VCards}, s.bot)
	if err != nil {
		return pipeline.Verdict{}, err
	}
	return s.pipe.Process(context.Background(), e, at)
}

func (s *Sim) person(m Member) int64 {
	if id, ok := s.persons[m.LID]; ok {
		return id
	}
	id := int64(len(s.persons) + 1)
	s.persons[m.LID] = id
	if m.PN != "" {
		s.persons[m.PN] = id
	}
	return id
}

func (s *Sim) indexes(group wa.JID) []*neardup.Index {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.groups[group]; ok {
		return []*neardup.Index{g.index}
	}
	return nil
}

type simGroups struct{ s *Sim }

func (g simGroups) Settings(_ context.Context, group wa.JID) (policy.Settings, bool, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	sg, ok := g.s.groups[group]
	if !ok {
		return policy.Settings{}, false, nil
	}
	return sg.settings.Clone(), true, nil
}

type simMembers struct{ s *Sim }

func (m simMembers) Sender(_ context.Context, group wa.JID, who envelope.Identity) (pipeline.Sender, int64, error) {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	pid := m.s.person(Member{LID: who.LID, PN: who.PN})
	if g, ok := m.s.groups[group]; ok {
		if rec, ok := g.members[who.LID]; ok {
			return pipeline.Sender{Member: rec, Known: true}, pid, nil
		}
	}
	return pipeline.Sender{}, pid, nil
}

type simReviews struct{ s *Sim }

func (r simReviews) Open(_ context.Context, n review.NewItem) (review.Item, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.reviews++
	return review.Item{NewItem: n, ID: int64(r.s.reviews)}, nil
}
