package fastlane

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/inbox"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/replay"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter/inboxenv"
)

// InboxReplay re-runs stored inbox rows (the 72 h buffer) through the current rules, corpus,
// weights and settings (DESIGN.md §18, F-089). It only reads: nothing is recorded, enqueued or
// sent, and the safety rails are a fresh in-memory copy. Verdicts show what the bot would decide
// now, so a rule or weight change can be compared against recent real traffic.
type InboxReplay struct {
	Pool    *pgxpool.Pool
	HashKey []byte
	Weights scoring.Weights // default scoring.DefaultWeights
	Log     zerolog.Logger
}

// Run replays rows received since since, for one group or every group, at most limit rows.
func (r InboxReplay) Run(ctx context.Context, group wa.JID, since time.Time, limit int32) (replay.Report, error) {
	clk := clock.Real{}
	hasher, err := blacklist.NewHasher(r.HashKey)
	if err != nil {
		return replay.Report{}, fmt.Errorf("number hash key: %w", err)
	}
	holder := rules.NewHolder(store.RulesLoader{Q: r.Pool}, clk, r.Log)
	if err := holder.Reload(ctx); err != nil {
		return replay.Report{}, fmt.Errorf("load rules: %w", err)
	}
	corpus := NewCorpus(r.Pool, clk, r.Log)
	if err := corpus.Reload(ctx); err != nil {
		return replay.Report{}, err
	}
	settings := policy.NewSettingsService(store.NewPolicyStore(r.Pool))
	groups := &groups{settings: settings, protected: func(context.Context, wa.JID) (bool, error) { return true, nil }}
	w := r.Weights
	if w.Version == "" {
		w = scoring.DefaultWeights()
	}
	members := IdentityMembers{IDs: identity.New(identity.NewPostgresStore(r.Pool), nil, nil, clk)}

	rows, err := db.New(r.Pool).ListInboxForReplay(ctx, db.ListInboxForReplayParams{Since: since, Chat: string(group), RowLimit: limit})
	if err != nil {
		return replay.Report{}, fmt.Errorf("list inbox: %w", err)
	}
	// The safety rails run on the messages' own time, so a burst replay of a day's rows counts bans
	// and deletions per real hour and minute, as they happened.
	railsClock := clock.NewFake(since)
	engines := map[wa.JID]*pipeline.Engine{}
	var rep replay.Report
	for _, row := range rows {
		bot := wa.JID(row.BotJid)
		en, ok := engines[bot]
		if !ok {
			set := pipeline.AnalyzerSet{Rules: holder, Hash: hasher.Hash, Groups: groups, Bot: bot, Corpus: corpus.Indexes, CorpusText: CorpusText(hasher, r.HashKey)}
			if en, err = pipeline.NewEngine(pipeline.EngineConfig{Analyzers: set.Analyzers(), Weights: w, Normalize: pipeline.NormalizeTexts, Clock: railsClock}); err != nil {
				return rep, err
			}
			engines[bot] = en
		}
		res, err := inboxenv.Decode(row.Kind, row.Raw, row.Info, bot)
		if errors.Is(err, inboxenv.ErrCorrupt) {
			r.Log.Warn().Err(err).Int64("inbox", row.ID).Msg("skipping a corrupt inbox row")
			continue
		}
		if err != nil {
			return rep, err
		}
		if res.Envelope == nil {
			continue
		}
		info, err := inbox.DecodeInfo(row.Info)
		if err != nil {
			r.Log.Warn().Err(err).Int64("inbox", row.ID).Msg("skipping an inbox row with unreadable info")
			continue
		}
		if info.Timestamp.After(railsClock.Now()) {
			railsClock.Advance(info.Timestamp.Sub(railsClock.Now()))
		}
		s, _, err := settings.Get(ctx, res.Envelope.Group)
		if err != nil {
			return rep, err
		}
		snd, _, err := members.Sender(ctx, res.Envelope.Group, res.Envelope.Sender)
		if err != nil {
			return rep, err
		}
		d, err := en.Evaluate(ctx, res.Envelope, info.Timestamp, s, snd)
		if err != nil {
			return rep, err
		}
		rep.Results = append(rep.Results, replay.Result{
			Record:  replay.Record{Kind: replay.KindMessage, Group: res.Envelope.Group, Sender: d.Verdict.Sender, ID: res.Envelope.MsgID},
			Verdict: d.Verdict,
		})
	}
	return rep, nil
}
