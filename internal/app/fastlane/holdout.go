package fastlane

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/neardup"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/replay"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// EvalGroup is the group an instance-wide evaluation runs in: no real group has this JID, so
// it gets the default settings and only the deployment-wide corpus applies.
const EvalGroup wa.JID = "0@g.us"

// evalSpacing is the time between two evaluated messages on the safety rails' clock, so the
// hourly ban and deletion limits never hold back a later message of a long evaluation.
const evalSpacing = 2 * time.Hour

// LabeledMessage is one message to evaluate and the label a person gave it ("spam" or "ham").
type LabeledMessage struct {
	Envelope *envelope.Envelope
	Label    string
}

// HoldoutReplay runs labelled messages through the fast lane with the current rules, corpus,
// weights and group settings, in memory (DESIGN.md §7.8, §18): the recall of an import's hold-out
// slice and the false-positive check of its ham records. Nothing is recorded, enqueued or sent,
// and the AI tier isn't called, so an uncertain message counts as sent to admins.
type HoldoutReplay struct {
	Pool    *pgxpool.Pool
	HashKey []byte
	Weights scoring.Weights // default scoring.DefaultWeights
	Log     zerolog.Logger
}

// Run evaluates msgs in group (EvalGroup for an instance-wide evaluation). extra are the
// normalized texts of corpus entries to match as well, in group's scope, as if they were stored:
// an import measures recall against the corpus it is about to write. Every message has its own
// sender, an established member, so neither flood limits across messages nor the new-member
// signal inflate recall.
func (r HoldoutReplay) Run(ctx context.Context, group wa.JID, extra []string, msgs []LabeledMessage) (replay.Report, error) {
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
	corpus.addExtra(group, extra)
	settings := policy.NewSettingsService(store.NewPolicyStore(r.Pool))
	s, _, err := settings.Get(ctx, group)
	if err != nil {
		return replay.Report{}, err
	}
	w := r.Weights
	if w.Version == "" {
		w = scoring.DefaultWeights()
	}
	start := clk.Now()
	railsClock := clock.NewFake(start)
	set := pipeline.AnalyzerSet{
		Rules: holder, Hash: hasher.Hash, Corpus: corpus.Indexes, CorpusText: CorpusText(hasher, r.HashKey),
		Groups: &groups{settings: settings, protected: func(context.Context, wa.JID) (bool, error) { return true, nil }},
	}
	en, err := pipeline.NewEngine(pipeline.EngineConfig{Analyzers: set.Analyzers(), Weights: w, Normalize: pipeline.NormalizeTexts, Clock: railsClock})
	if err != nil {
		return replay.Report{}, err
	}
	var rep replay.Report
	for i, m := range msgs {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		railsClock.Advance(evalSpacing)
		e := *m.Envelope
		e.Group = group
		e.MsgID = wa.MessageID(fmt.Sprintf("EVAL%06d", i))
		e.Sender = envelope.Identity{LID: wa.JID(fmt.Sprintf("%d@lid", 900000000000+i))}
		snd := pipeline.Sender{Known: true, Member: policy.MemberRecord{
			LID: string(e.Sender.LID), Role: policy.RoleMember, JoinedAt: start.Add(-365 * 24 * time.Hour), PreExisting: true,
		}}
		d, err := en.Evaluate(ctx, &e, railsClock.Now(), s, snd)
		if err != nil {
			return rep, err
		}
		rep.Results = append(rep.Results, replay.Result{
			Record:  replay.Record{Kind: replay.KindMessage, Group: group, Sender: e.Sender.LID, ID: e.MsgID, Expect: m.Label},
			Verdict: d.Verdict,
		})
	}
	return rep, nil
}

// addExtra adds normalized texts to the index in group's scope (the global index for EvalGroup),
// in memory only.
func (c *Corpus) addExtra(group wa.JID, texts []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ix := c.global
	if group != EvalGroup {
		if c.groups[group] == nil {
			c.groups[group] = neardup.NewIndex(neardup.DefaultThresholds())
		}
		ix = c.groups[group]
	}
	for i, t := range texts {
		ix.Add(fmt.Sprintf("extra-%d", i), neardup.Compute(neardup.Key(t)))
	}
}
