package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/aimetrics"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/aistore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/budget"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/gateway"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/app/fastlane"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/config"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/logging"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	reviewpg "github.com/RayanAlyasi/AntiSpam-Bot/internal/review/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewjobs"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// fastlaneStack is the detection fast lane in this process (F-088): the inbox's moderation sink
// with one lane per connected bot number, the confirmed-spam corpus index and, when configured,
// the AI tier for the uncertain band.
type fastlaneStack struct {
	lanes    *fastlane.Lanes
	chat     *chatSink
	corpus   *fastlane.Corpus
	reviews  *fastlane.Reviews
	evidence review.EvidenceStore
	pool     *pgxpool.Pool
	settings *policy.SettingsService // shared by the lanes, reviews and the chat handler
	log      zerolog.Logger
}

// checkHashKey enforces HASH_KEY: without a valid key the bot can't match blacklisted or
// advertised numbers, so it refuses to start once a number is paired. Before pairing it only
// warns, so a fresh install can come up and be configured. ok is false when serve must exit.
func checkHashKey(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, log zerolog.Logger) (key []byte, ok bool) {
	key, err := cfg.NumberHashKey()
	if err == nil {
		return key, true
	}
	bots, lerr := store.NewBots(pool).List(ctx)
	if lerr != nil {
		log.Error().Err(lerr).Msg("list bots")
		return nil, false
	}
	if len(bots) > 0 {
		log.Error().Err(err).Msg("refusing to start: a bot number is paired, and moderation needs HASH_KEY")
		return nil, false
	}
	log.Error().Err(err).Msg("no bot number is paired yet, so starting without moderation; set HASH_KEY before pairing, or the bot will refuse to start")
	return nil, true
}

// newFastlane assembles the fast lane. getenv reads the AI API key.
func newFastlane(cfg config.Config, key []byte, pool *pgxpool.Pool, getenv func(string) string, logs *logging.Loggers, ws *waStack, lids *lidSwitch,
	reg prometheus.Registerer, metrics *fastlane.Metrics) (*fastlaneStack, error) {
	clk := clock.Real{}
	log := logs.For("fastlane").Hook(fastlane.CallerHook{})
	var verifier *gateway.Verifier
	if cfg.AI.Enabled() {
		m, err := aimetrics.New(reg)
		if err != nil {
			return nil, fmt.Errorf("ai metrics: %w", err)
		}
		cls, err := gateway.New(aiConfig(cfg.AI), getenv, clk, m)
		if err != nil {
			return nil, fmt.Errorf("ai classifier: %w", err)
		}
		results, err := gateway.NewResultCache(aistore.NewCache(pool), clk)
		if err != nil {
			return nil, fmt.Errorf("ai result cache: %w", err)
		}
		bud, err := budget.New(aistore.NewBudget(pool), clk, budget.DefaultLimits(), nil, m)
		if err != nil {
			return nil, fmt.Errorf("ai budget: %w", err)
		}
		if verifier, err = gateway.NewVerifier(gateway.VerifierOptions{Classifier: cls, Cache: results, Budget: bud, Metrics: m}); err != nil {
			return nil, fmt.Errorf("ai verifier: %w", err)
		}
	}
	corpus := fastlane.NewCorpus(pool, clk, log)
	hasher, err := blacklist.NewHasher(key)
	if err != nil {
		return nil, fmt.Errorf("number hash key: %w", err)
	}
	evidence := reviewpg.Evidence{DB: pool}
	learning := review.NewLearning(review.LearningDeps{
		Evidence: evidence,
		Corpus:   &fastlane.CorpusLearning{Pool: pool, Clock: clk, Hasher: hasher, Key: key, Index: corpus, Log: log},
		Standing: noStanding{},
		Clock:    clk,
	})
	settings := policy.NewSettingsService(store.NewPolicyStore(pool))
	// A group is protected when the bot is an admin there (wp-f's groups table): the same rule
	// the inbox router uses to send its messages to moderation.
	protected := func(ctx context.Context, chat wa.JID) (bool, error) {
		if !chat.IsGroup() {
			return false, nil
		}
		g, ok, err := ws.groups.Group(ctx, chat)
		return ok && g.Status == identity.GroupActive, err
	}
	var lanes *fastlane.Lanes
	reviews, err := fastlane.NewReviews(fastlane.ReviewDeps{
		Pool: pool, Clock: clk, Log: log, Settings: settings, Queue: actions.NewPGStore(pool, clk),
		BotFor:  func(ctx context.Context, chat wa.JID) (wa.JID, error) { return botFor(ctx, ws, lids, chat) },
		Persons: identityPersons{ws.ids}, Admins: identityAdmins{ws.ids}, Learner: learning,
		Protected: protected,
		Wake:      ws.wakeActions,
	})
	if err != nil {
		return nil, err
	}
	hook := reviews.Queue.OnFinished
	ws.finished.Store(&hook)
	lanes = fastlane.NewLanes(fastlane.Deps{
		Pool: pool, Clock: clk, HashKey: key, Log: log, AI: verifier, Corpus: corpus.Indexes, Reviews: reviews, Protected: protected,
		Settings: settings,
		Metrics:  metrics,
		// The executor's posting rules come from the settings documents (unknown groups count as
		// protected and silent), and losing admin rights marks the group as waiting for them.
		Groups: actions.PGGroups{DB: pool, Log: log, OnNotAdmin: func(ctx context.Context, _, group wa.JID) error {
			g, ok, err := ws.groups.Group(ctx, group)
			if err != nil || !ok {
				return err
			}
			g.BotIsAdmin, g.Status = false, identity.GroupPendingAdmin
			return ws.groups.UpsertGroup(ctx, g)
		}},
		Members: fastlane.IdentityMembers{IDs: ws.ids}, Admins: identityAdmins{ws.ids},
		// The connector role's actions.Runner runs one executor per connected bot (WP-H).
		ExternalExecutor: true,
		Wake:             ws.wakeActions,
	}, func(bot wa.JID) (wa.WAClient, bool) {
		m := lids.m.Load() // the connector role's manager, when this process runs it
		if m == nil {
			return nil, false
		}
		return m.Client(bot)
	})
	cs := newChatSink(pool, ws, settings, reviews.Service, learning, reviews.Queue, cfg.OperatorPhones)
	cs.log, cs.metrics = logs.For("chat").Hook(fastlane.CallerHook{}), metrics
	return &fastlaneStack{lanes: lanes, chat: cs, corpus: corpus, reviews: reviews, evidence: evidence, pool: pool, settings: settings, log: log}, nil
}

// role runs the lanes (their executors and rules listeners) and the corpus refresh.
func (f *fastlaneStack) role() role {
	return role{name: roleWorkers + "/fastlane", run: func(ctx context.Context) error {
		var wg sync.WaitGroup
		wg.Go(func() {
			if err := f.corpus.Run(ctx); err != nil {
				f.log.Error().Err(err).Msg("corpus refresh stopped")
			}
		})
		wg.Go(func() {
			if err := f.reviews.Run(ctx, clock.Real{}); err != nil {
				f.log.Error().Err(err).Msg("review sweep stopped")
			}
		})
		// Settings changed by any process (a command, the website) reach the cache at once.
		wg.Go(func() { f.listenSettings(ctx) })
		err := f.lanes.Run(ctx)
		wg.Wait()
		return err
	}}
}

// listenSettings drops cached group settings whenever any process saves a change, and drops them
// all after a lost connection, since changes made meanwhile were missed.
func (f *fastlaneStack) listenSettings(ctx context.Context) {
	ps := store.NewPolicyStore(f.pool)
	for {
		// Changes saved while the listener was down (or before it started) were missed.
		f.settings.InvalidateAll()
		err := ps.ListenSettings(ctx, f.settings.Invalidate)
		if ctx.Err() != nil {
			return
		}
		f.log.Warn().Err(err).Msg("settings listener lost its connection; retrying")
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// registerJobs adds the review timeouts and evidence purge (wp-k) and the AI result cache purge
// (wp-j) to the River client.
func (f *fastlaneStack) registerJobs(workers *river.Workers, periodic *[]*river.PeriodicJob) error {
	if err := reviewjobs.Register(f.reviews.Service, f.evidence, clock.Real{})(workers, periodic); err != nil {
		return err
	}
	return aistore.RegisterPurge(workers, periodic, aistore.NewCache(f.pool), clock.Real{})
}

// botFor returns the bot number that serves a chat: the group's own bot from the groups table,
// else (a DM, or a review group not tracked yet) the one connected bot, or the one paired bot.
func botFor(ctx context.Context, ws *waStack, lids *lidSwitch, chat wa.JID) (wa.JID, error) {
	if chat.IsGroup() {
		if g, ok, err := ws.groups.Group(ctx, chat); err != nil {
			return "", err
		} else if ok && g.Bot != "" {
			return g.Bot, nil
		}
	}
	if m := lids.m.Load(); m != nil {
		if bots := m.Connected(); len(bots) == 1 {
			return bots[0], nil
		}
	}
	bots, err := store.NewBots(ws.pool).List(ctx)
	if err != nil {
		return "", err
	}
	if len(bots) == 1 {
		return bots[0].JID, nil
	}
	return "", fmt.Errorf("no single bot number serves %s here", chat)
}

// identityPersons maps a JID to its person, creating it if needed (bans decided in reviews).
type identityPersons struct{ ids *identity.Service }

func (p identityPersons) PersonID(ctx context.Context, jid wa.JID) (int64, error) {
	person, err := p.ids.Ensure(ctx, jid, identity.SourceEvent)
	return int64(person.ID), err
}

// noStanding: trust restore and strikes come with the trust store; an overturn still retracts
// the corpus entry and deletes the evidence text.
type noStanding struct{}

func (noStanding) RemoveStrike(context.Context, wa.JID, wa.JID, wa.MessageID) error { return nil }
func (noStanding) RestoreTrust(context.Context, wa.JID, wa.JID) error               { return nil }
func (noStanding) RecordFalsePositive(context.Context, wa.JID, wa.MessageRef, wa.JID) error {
	return nil
}

// aiConfig maps the instance configuration's ai section to the gateway's.
func aiConfig(c config.AI) ai.Config {
	return ai.Config{
		Provider: c.Provider, BaseURL: c.BaseURL, Model: c.Model, APIKeyEnv: c.APIKeyEnv, Timeout: c.Timeout,
		StructuredOutput: ai.OutputSetting(c.StructuredOutput),
	}.WithDefaults()
}

// identityAdmins answers review votes' "is this person an admin of this group" from the members
// table, which the connector keeps current from group events (DESIGN.md §9.5).
type identityAdmins struct{ ids *identity.Service }

func (a identityAdmins) IsAdmin(ctx context.Context, group, person wa.JID) (bool, error) {
	p, ok, err := a.ids.MatchPerson(ctx, person)
	if err != nil || !ok {
		return false, err
	}
	m, ok, err := a.ids.Member(ctx, group, p.ID)
	if err != nil || !ok {
		return false, err
	}
	return m.Role == identity.RoleAdmin || m.Role == identity.RoleSuperAdmin, nil
}
