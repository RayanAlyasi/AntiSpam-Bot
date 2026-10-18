package fastlane

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	reviewpg "github.com/RayanAlyasi/AntiSpam-Bot/internal/review/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/queue"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// SweepEvery is how often sent review messages are mapped to their reviews, so admins'
// reactions and replies on them count as votes (review/queue.Queue.Sweep).
const SweepEvery = 2 * time.Second

// Reviews is the admin review service (wp-k, DESIGN.md §9.5) and its bridge to the action queue,
// shared by every lane of the process. Its store and queue write through the message's
// transaction when a detection opens an item, so the item, its post and the detection commit
// together; votes, timeouts and commands use the pool.
type Reviews struct {
	Service *review.Service
	Queue   *queue.Queue
	log     zerolog.Logger
}

// ReviewDeps are what NewReviews needs. BotFor and Persons are required.
type ReviewDeps struct {
	Pool     *pgxpool.Pool
	Clock    clock.Clock
	Log      zerolog.Logger
	Settings *policy.SettingsService
	Queue    *actions.PGStore
	// BotFor returns the bot number that serves a chat (a group or a DM).
	BotFor func(ctx context.Context, chat wa.JID) (wa.JID, error)
	// Persons maps a JID to its person ID, creating the person if needed (identity).
	Persons   queue.Persons
	Admins    review.Admins
	Learner   review.Learner
	Protected func(ctx context.Context, chat wa.JID) (bool, error)
	// Wake nudges the executors after an enqueue. Optional.
	Wake func()
}

// NewReviews wires the review service.
func NewReviews(d ReviewDeps) (*Reviews, error) {
	if d.Pool == nil || d.Clock == nil || d.Settings == nil || d.Queue == nil || d.BotFor == nil || d.Persons == nil {
		return nil, errors.New("fastlane: reviews need the pool, clock, settings, queue, BotFor and Persons")
	}
	if d.Admins == nil {
		d.Admins = noAdmins{}
	}
	if d.Learner == nil {
		d.Learner = noLearning{}
	}
	conn := pgstore.Conn{Pool: d.Pool}
	bans := store.NewPolicyStore(d.Pool)
	enq := txQueue{d.Queue}
	r := &Reviews{log: d.Log}
	r.Queue = &queue.Queue{
		Queue:    enq,
		BotFor:   d.BotFor,
		Bans:     policy.NewBans(bans, kicker{q: enq, botFor: d.BotFor}, d.Clock),
		BanStore: bans,
		Persons:  d.Persons,
		Marks:    reviewpg.BlacklistBans{DB: d.Pool},
		RejoinBlock: func(ctx context.Context, group wa.JID) (bool, error) {
			s, _, err := d.Settings.Get(ctx, group)
			return s.Enforcement.Ban.RejoinBlock, err
		},
		Posted: func(ctx context.Context, p review.Posted) error { return r.Service.RecordPosted(ctx, p) },
		Clock:  d.Clock,
		Wake:   d.Wake,
	}
	r.Service = review.NewService(review.Deps{
		Store:     reviewpg.Reviews{DB: conn},
		Settings:  review.PolicySettings{Docs: d.Settings},
		Admins:    d.Admins,
		Actions:   r.Queue,
		Learner:   d.Learner,
		Clock:     d.Clock,
		Protected: d.Protected,
	})
	return r, nil
}

// Open implements pipeline.Reviews.
func (r *Reviews) Open(ctx context.Context, n review.NewItem) (review.Item, error) {
	return r.Service.Open(ctx, n)
}

// Run maps sent review posts to their reviews until ctx ends. Timeouts run as a River job
// (review/reviewjobs).
func (r *Reviews) Run(ctx context.Context, clk clock.Clock) error {
	for {
		if err := r.Queue.Sweep(ctx); err != nil && ctx.Err() == nil {
			r.log.Warn().Err(err).Msg("review sweep")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-clk.After(SweepEvery):
		}
	}
}

// txQueue enqueues in the context's transaction when there is one (a detection opening a
// review), and on the pool otherwise.
type txQueue struct{ q *actions.PGStore }

func (t txQueue) Enqueue(ctx context.Context, a actions.Action) (actions.EnqueueResult, error) {
	if tx, ok := pgstore.TxFrom(ctx); ok {
		return t.q.EnqueueTx(ctx, tx, a)
	}
	return t.q.Enqueue(ctx, a)
}

func (t txQueue) Get(ctx context.Context, id int64) (actions.Action, error) { return t.q.Get(ctx, id) }

// kicker enqueues the kick of a local ban (policy.Kicker) on the serving bot's queue.
type kicker struct {
	q      txQueue
	botFor func(ctx context.Context, chat wa.JID) (wa.JID, error)
}

func (k kicker) Kick(ctx context.Context, group, member wa.JID, priority int, dedupeKey string) error {
	bot, err := k.botFor(ctx, group)
	if err != nil {
		return err
	}
	_, err = k.q.Enqueue(ctx, actions.Action{
		Bot: bot, Chat: group, Kind: actions.KindKick, Priority: actions.Priority(priority),
		Payload: actions.Payload{JIDs: []wa.JID{member}}, DedupeKey: dedupeKey,
	})
	return err
}
