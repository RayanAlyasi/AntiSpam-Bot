// Package fastlane assembles the production fast lane (DESIGN.md §4, §7.1, F-088) for one bot
// number: the analyzers, scoring, the AI check, routing, the Postgres store and action queue,
// admin reviews and the paced executor. The connector's inbox worker hands it envelopes; it
// records detections and enqueues actions in one transaction, and the executor performs them.
package fastlane

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/gateway"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/neardup"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/queue"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Deps are what the fast lane is built from. Pool, Clock, Bot and Client are required; the rest
// have v0.1 defaults, noted on each field, that the owning packages replace as they land.
type Deps struct {
	Pool    *pgxpool.Pool
	Clock   clock.Clock
	HashKey []byte      // the deployment's number-hash key (config.NumberHashKey), ≥ 16 bytes
	Bot     wa.JID      // the bot number this lane acts as
	Client  wa.WAClient // that number's WhatsApp connection (the whatsmeow adapter)
	Log     zerolog.Logger

	// Settings is the process's group settings service. Share one between the lanes, reviews and
	// the chat handler, and keep it fresh with store.PolicyStore.ListenSettings, so an admin's
	// `.mode` takes effect at once. Nil: the lane builds its own (tests).
	Settings *policy.SettingsService

	Weights scoring.Weights   // default scoring.DefaultWeights
	AI      *gateway.Verifier // nil: the AI tier is off and uncertain messages go to admins
	Actions *actions.Config   // nil: actions.DefaultConfig

	// Analyzers run besides the v0.1 set (rules, blacklist, domains, exact hashes, corpus).
	Analyzers []pipeline.Analyzer
	// Corpus gives the confirmed-spam indexes for a group (F-113/F-114). Nil: no corpus yet.
	Corpus neardup.Indexes
	// Members says who sent a message (wp-f identity and members). Nil: every sender is unknown,
	// which is the conservative reading only for detection; routing then treats them as new.
	Members pipeline.Members
	// Protected says which groups are moderated (wp-f groups table). Nil: a group is protected
	// once it has a stored settings document, e.g. after an admin's `.mode shadow`.
	Protected func(ctx context.Context, group wa.JID) (bool, error)
	// Reviews is the review service shared by every lane (NewReviews). Nil: the lane builds its
	// own from Persons, Admins and Learner, serving only its bot.
	Reviews *Reviews
	// Persons maps a JID to its person for bans decided in reviews (identity). Nil: such bans
	// fail until it's wired.
	Persons queue.Persons
	// Admins answers "is this person an admin of this group" for review votes (wp-f
	// participant cache). Nil: nobody, so votes are refused until it's wired.
	Admins review.Admins
	// Learner turns review decisions into learning (wp-k). Nil: decisions aren't learned from.
	Learner review.Learner
	// Groups is the executor's view of a chat's posting rules. Nil: derived from Protected and
	// each group's silent setting.
	Groups actions.Groups
	Alerts actions.Alerter

	// Metrics counts panics and errors (panics_total, errors_total). Nil: not counted.
	Metrics *Metrics
	// ExternalExecutor leaves performing the queued actions to someone else (an actions.Runner
	// that serves every connected bot): the lane builds no executor and wakes through Wake.
	ExternalExecutor bool
	// Wake nudges the external executor after an enqueue commits. Optional.
	Wake func()
}

// Lane is one bot number's fast lane.
type Lane struct {
	Rules    *rules.Holder
	Pipeline *pipeline.Pipeline
	Reviews  *Reviews
	Executor *actions.Executor
	Settings *policy.SettingsService
	Queue    *actions.PGStore

	ownReviews bool
	metrics    *Metrics
	wake       func()
	pool       *pgxpool.Pool
	clock      clock.Clock
	log        zerolog.Logger
}

// New assembles the fast lane and loads the rules snapshot, so Process can run at once.
func New(ctx context.Context, d Deps) (*Lane, error) {
	if d.Pool == nil || d.Clock == nil || d.Bot == "" || d.Client == nil {
		return nil, errors.New("fastlane: pool, clock, bot and client are required")
	}
	hasher, err := blacklist.NewHasher(d.HashKey)
	if err != nil {
		return nil, fmt.Errorf("fastlane: number hash key: %w", err)
	}
	if d.Weights.Version == "" {
		d.Weights = scoring.DefaultWeights()
	}
	cfg := actions.DefaultConfig()
	if d.Actions != nil {
		cfg = *d.Actions
	}
	settings := d.Settings
	if settings == nil {
		settings = policy.NewSettingsService(store.NewPolicyStore(d.Pool))
	}
	groups := &groups{settings: settings, protected: d.Protected}

	queue := actions.NewPGStore(d.Pool, d.Clock)
	execGroups := d.Groups
	if execGroups == nil {
		execGroups = posting{groups}
	}
	var exec *actions.Executor
	wake := d.Wake
	if !d.ExternalExecutor {
		if exec, err = actions.NewExecutor(cfg, actions.Deps{Bot: d.Bot, Client: d.Client, Store: queue, Clock: d.Clock, Groups: execGroups, Alerts: d.Alerts, Log: d.Log}); err != nil {
			return nil, fmt.Errorf("fastlane: executor: %w", err)
		}
		wake = exec.Wake
	}
	if wake == nil {
		wake = func() {}
	}

	holder := rules.NewHolder(store.RulesLoader{Q: d.Pool}, d.Clock, d.Log)
	if err := holder.Reload(ctx); err != nil {
		return nil, fmt.Errorf("fastlane: load rules: %w", err)
	}
	set := pipeline.AnalyzerSet{Rules: holder, Hash: hasher.Hash, Groups: groups, Bot: d.Bot, Corpus: d.Corpus, CorpusText: CorpusText(hasher, d.HashKey)}
	analyzers := append(set.Analyzers(), d.Analyzers...)
	var verify pipeline.Verifier
	if d.AI != nil {
		verify = pipeline.AIVerifier{Gateway: d.AI}
	}
	engine, err := pipeline.NewEngine(pipeline.EngineConfig{Analyzers: analyzers, Weights: d.Weights, Normalize: pipeline.NormalizeTexts, Verify: verify, Clock: d.Clock})
	if err != nil {
		return nil, fmt.Errorf("fastlane: engine: %w", err)
	}

	reviews, ownReviews := d.Reviews, false
	if reviews == nil {
		persons := d.Persons
		if persons == nil {
			persons = noPersons{}
		}
		reviews, err = NewReviews(ReviewDeps{
			Pool: d.Pool, Clock: d.Clock, Log: d.Log, Settings: settings, Queue: queue,
			BotFor:  func(context.Context, wa.JID) (wa.JID, error) { return d.Bot, nil },
			Persons: persons, Admins: d.Admins, Learner: d.Learner, Protected: groups.isProtected, Wake: wake,
		})
		if err != nil {
			return nil, err
		}
		ownReviews = true
	}

	members := d.Members
	if members == nil {
		members = unknownMembers{}
	}
	pipe, err := pipeline.New(pipeline.Config{
		Engine: engine, Groups: groups, Members: members, Bot: d.Bot, Clock: d.Clock,
		Store: pgstore.New(d.Pool, func(ctx context.Context, tx db.DBTX, a actions.Action) error {
			_, err := queue.EnqueueTx(ctx, tx, a)
			return err
		}, reviews),
		RuleVersion: d.Weights.Version,
		Committed:   wake,
		// Every deletion keeps a redacted evidence snapshot (text and thumbnail, 30 days, F-114),
		// saved in the detection's transaction.
		// The 30 days count from the deletion, not from when the message was sent.
		Evidence: func(e *envelope.Envelope, detID int64, _ time.Time) (review.Snapshot, bool) {
			return review.SnapshotOf(e, detID, "detection", d.Clock.Now()), true
		},
	})
	if err != nil {
		return nil, fmt.Errorf("fastlane: pipeline: %w", err)
	}
	return &Lane{
		Rules: holder, Pipeline: pipe, pool: d.Pool, clock: d.Clock, log: d.Log, Reviews: reviews, ownReviews: ownReviews,
		Executor: exec, Settings: settings, Queue: queue, metrics: d.Metrics, wake: wake,
	}, nil
}

// Run keeps the rules snapshot current (LISTEN rules_changed; a (re)connect reloads everything)
// and performs the queued actions, until ctx ends.
func (l *Lane) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	changes := make(chan struct{}, 1)
	n := 3
	errs := make(chan error, 4)
	go func() { errs <- store.ListenRulesChanged(ctx, l.pool, l.clock, l.log, changes) }()
	go func() { errs <- l.Rules.Run(ctx, changes) }()
	if l.Executor != nil {
		go func() { errs <- l.runExecutor(ctx) }()
	} else {
		n--
	}
	if l.ownReviews {
		n++
		go func() { errs <- l.Reviews.Run(ctx, l.clock) }()
	}
	var first error
	for range n {
		if err := <-errs; err != nil && first == nil && ctx.Err() == nil {
			first = err
			cancel()
		}
	}
	return first
}

// runExecutor performs the queued actions until ctx ends. A panic in the executor is logged,
// counted and the executor started again after a pause, so one bad action can't stop all of them.
func (l *Lane) runExecutor(ctx context.Context) error {
	for {
		err := Recovered(l.log, l.metrics, "executor", func() error { return l.Executor.Run(ctx) })
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && !errors.Is(err, actions.ErrExecutorRunning) {
			l.metrics.failed("actions")
			l.log.Error().Err(err).Msg("executor stopped; restarting")
		} else if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-l.clock.After(time.Second):
		}
	}
}

// Wake nudges whoever performs this lane's queued actions.
func (l *Lane) Wake() { l.wake() }

// Process runs one received group message through the fast lane. at is when it was sent.
func (l *Lane) Process(ctx context.Context, e *envelope.Envelope, at time.Time) (pipeline.Verdict, error) {
	return l.Pipeline.Process(ctx, e, at)
}

type groups struct {
	settings  *policy.SettingsService
	protected func(ctx context.Context, group wa.JID) (bool, error)
}

func (g *groups) Settings(ctx context.Context, group wa.JID) (policy.Settings, bool, error) {
	if !group.IsGroup() {
		return policy.Settings{}, false, nil
	}
	s, version, err := g.settings.Get(ctx, group)
	if err != nil {
		return policy.Settings{}, false, err
	}
	if g.protected != nil {
		ok, err := g.protected(ctx, group)
		return s, ok, err
	}
	return s, version > 0, nil
}

func (g *groups) isProtected(ctx context.Context, chat wa.JID) (bool, error) {
	_, ok, err := g.Settings(ctx, chat)
	return ok, err
}

// posting derives the executor's posting rules: nothing is posted in a protected group whose
// silent setting is on (DESIGN.md §9.7), except shadow reactions while they're on (§8.4).
type posting struct{ g *groups }

func (p posting) Posting(ctx context.Context, chat wa.JID) (actions.Posting, error) {
	s, ok, err := p.g.Settings(ctx, chat)
	if err != nil || !ok {
		return actions.Posting{}, err
	}
	return actions.Posting{Protected: true, Silent: s.Silent, ShadowReactions: s.Mode == policy.ModeShadow && s.Shadow.Reactions}, nil
}

func (posting) NotAdmin(context.Context, wa.JID, wa.JID) error { return nil }

type unknownMembers struct{}

func (unknownMembers) Sender(context.Context, wa.JID, envelope.Identity) (pipeline.Sender, int64, error) {
	return pipeline.Sender{}, 0, nil
}

type noPersons struct{}

func (noPersons) PersonID(context.Context, wa.JID) (int64, error) { return 0, queue.ErrNoPersons }

type noAdmins struct{}

func (noAdmins) IsAdmin(context.Context, wa.JID, wa.JID) (bool, error) { return false, nil }

type noLearning struct{}

func (noLearning) ConfirmSpam(context.Context, review.Confirmation) error { return nil }
func (noLearning) Overturn(context.Context, review.Overturn) error        { return nil }
