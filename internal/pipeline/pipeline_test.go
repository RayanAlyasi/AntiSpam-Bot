package pipeline

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const (
	bot   wa.JID = "100@lid"
	group wa.JID = "1@g.us"
)

type groupsFunc func(wa.JID) (policy.Settings, bool)

func (f groupsFunc) Settings(_ context.Context, g wa.JID) (policy.Settings, bool, error) {
	s, ok := f(g)
	return s, ok, nil
}

type members map[wa.JID]Sender

func (m members) Sender(_ context.Context, _ wa.JID, who envelope.Identity) (Sender, int64, error) {
	if who.LID == "anon@lid" {
		return Sender{}, 0, nil // identity not resolved: no person ID
	}
	return m[who.LID], 42, nil
}

type reviews struct{ opened []review.NewItem }

func (r *reviews) Open(_ context.Context, n review.NewItem) (review.Item, error) {
	r.opened = append(r.opened, n)
	return review.Item{NewItem: n}, nil
}

type failingStore struct{ *MemStore }

func (f failingStore) InTx(ctx context.Context, fn func(context.Context, Tx) error) error {
	return f.MemStore.InTx(ctx, func(ctx context.Context, tx Tx) error {
		if err := fn(ctx, tx); err != nil {
			return err
		}
		return errors.New("commit failed")
	})
}

func newPipeline(t *testing.T, s policy.Settings, m members, st Store) (*Pipeline, *reviews, *int) {
	t.Helper()
	en, err := NewEngine(EngineConfig{Analyzers: []Analyzer{wordAnalyzer{}}, Weights: scoring.DefaultWeights(), Normalize: NormalizeTexts, Clock: clock.NewFake(t0)})
	if err != nil {
		t.Fatal(err)
	}
	rv := &reviews{}
	switch ms := st.(type) {
	case *MemStore:
		ms.Reviews = rv
	case failingStore:
		ms.Reviews = rv
	}
	wakes := 0
	p, err := New(Config{
		Engine: en, Groups: groupsFunc(func(g wa.JID) (policy.Settings, bool) { return s, g == group }),
		Members: m, Store: st, Bot: bot, Clock: clock.NewFake(t0), RuleVersion: "test", Committed: func() { wakes++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	return p, rv, &wakes
}

func msg(id wa.MessageID, sender wa.JID, text string) *envelope.Envelope {
	return &envelope.Envelope{
		Bot: bot, Group: group, Sender: envelope.Identity{LID: sender}, MsgID: id, Kind: envelope.KindText,
		Texts: []envelope.TextSurface{{Source: "body", Raw: text}},
	}
}

func kinds(as []actions.Action) []actions.Kind {
	var out []actions.Kind
	for _, a := range as {
		out = append(out, a.Kind)
	}
	return out
}

func TestPipelineWritesDetectionBanAndActionsTogether(t *testing.T) {
	st := NewMemStore()
	p, rv, wakes := newPipeline(t, settings(t, policy.PresetStandard, policy.ModeEnforce), members{}, st)
	v, err := p.Process(t.Context(), msg("M1", "9@lid", "buy spam now"), t0)
	if err != nil {
		t.Fatal(err)
	}
	dets, bans, acts := st.Snapshot()
	if len(dets) != 1 || dets[0].Mode != policy.DetectionEnforce || len(bans) != 1 || !bans[0].Active {
		t.Fatalf("detections %+v, bans %+v: want one enforce detection and one active ban", dets, bans)
	}
	if got := kinds(acts); !slices.Equal(got, []actions.Kind{actions.KindRevoke, actions.KindKick}) {
		t.Fatalf("enqueued %v, want revoke and kick", got)
	}
	if acts[0].Payload.Target != (wa.MessageRef{Sender: "9@lid", ID: "M1"}) || !slices.Equal(acts[1].Payload.JIDs, []wa.JID{"9@lid"}) {
		t.Fatalf("payloads %+v / %+v target the wrong message or member", acts[0].Payload, acts[1].Payload)
	}
	if acts[0].ReasonDetectionID != dets[0].ID || *bans[0].DetectionID != dets[0].ID {
		t.Fatal("actions and the ban must link to the detection that caused them")
	}
	if len(rv.opened) != 0 || *wakes != 1 || v.Route != RouteAutoBan {
		t.Fatalf("reviews %d, wakes %d, route %s; want none, one wake, auto_ban", len(rv.opened), *wakes, v.Route)
	}

	// A redelivery writes nothing again.
	v2, err := p.Process(t.Context(), msg("M1", "9@lid", "buy spam now"), t0)
	if err != nil {
		t.Fatal(err)
	}
	dets, bans, acts = st.Snapshot()
	if !v2.Duplicate || len(dets) != 1 || len(bans) != 1 || len(acts) != 2 {
		t.Fatalf("after a redelivery: duplicate=%v, %d detections, %d bans, %d actions; want true, 1, 1, 2",
			v2.Duplicate, len(dets), len(bans), len(acts))
	}
}

func TestPipelineEstablishedMemberGetsReview(t *testing.T) {
	st := NewMemStore()
	m := members{"2@lid": {Known: true, Member: policy.MemberRecord{LID: "2@lid", Role: policy.RoleMember, JoinedAt: t0.AddDate(-1, 0, 0), PreExisting: true}}}
	p, rv, _ := newPipeline(t, settings(t, policy.PresetStandard, policy.ModeEnforce), m, st)
	if _, err := p.Process(t.Context(), msg("M2", "2@lid", "buy spam now"), t0); err != nil {
		t.Fatal(err)
	}
	_, bans, acts := st.Snapshot()
	if len(bans) != 0 || !slices.Equal(kinds(acts), []actions.Kind{actions.KindRevoke}) {
		t.Fatalf("bans %d, actions %v: an established member is deleted, never banned (DESIGN §8.1)", len(bans), kinds(acts))
	}
	if len(rv.opened) != 1 || rv.opened[0].Kind != review.KindBanDecision || rv.opened[0].Proposed != review.DecisionBan ||
		!rv.opened[0].Evidence.MessageDeleted || !rv.opened[0].Subject.Established {
		t.Fatalf("reviews %+v, want one ban decision about a deleted message from an established member", rv.opened)
	}
}

func TestPipelineShadowAndUnprotected(t *testing.T) {
	st := NewMemStore()
	p, rv, wakes := newPipeline(t, settings(t, policy.PresetStandard, policy.ModeShadow), members{}, st)
	v, err := p.Process(t.Context(), msg("M3", "9@lid", "buy spam now"), t0)
	if err != nil {
		t.Fatal(err)
	}
	dets, bans, acts := st.Snapshot()
	if len(acts) != 0 || len(bans) != 0 || len(rv.opened) != 0 || *wakes != 0 {
		t.Fatalf("shadow mode wrote %d actions, %d bans, %d reviews; it must act on nothing (DESIGN §8.4)", len(acts), len(bans), len(rv.opened))
	}
	if len(dets) != 1 || dets[0].Mode != policy.DetectionShadow || !slices.Equal(v.Actions, []ActionKind{ActionDelete, ActionBan}) {
		t.Fatalf("shadow detection %+v / verdict actions %v: want the would-be delete and ban recorded", dets, v.Actions)
	}

	other := msg("M4", "9@lid", "buy spam now")
	other.Group = "2@g.us"
	if v, err := p.Process(t.Context(), other, t0); err != nil || v.Route != RouteNone {
		t.Fatalf("unprotected group: %+v, %v; want route none", v, err)
	}
}

func TestPipelineRollsBackOnFailedCommit(t *testing.T) {
	st := NewMemStore()
	p, rv, wakes := newPipeline(t, settings(t, policy.PresetStandard, policy.ModeEnforce), members{}, failingStore{st})
	if _, err := p.Process(t.Context(), msg("M5", "9@lid", "buy spam now"), t0); err == nil {
		t.Fatal("a failed transaction must be an error")
	}
	dets, bans, acts := st.Snapshot()
	if len(dets)+len(bans)+len(acts)+len(rv.opened)+*wakes != 0 {
		t.Fatal("a failed transaction must leave no detection, ban, action or review behind")
	}
	// The retry, on a working store, does the work exactly once.
	p2, _, _ := newPipeline(t, settings(t, policy.PresetStandard, policy.ModeEnforce), members{}, st)
	if _, err := p2.Process(t.Context(), msg("M5", "9@lid", "buy spam now"), t0); err != nil {
		t.Fatal(err)
	}
	if _, _, acts := st.Snapshot(); len(acts) != 2 {
		t.Fatalf("retry enqueued %d actions, want 2", len(acts))
	}
}

func TestPipelineEditIsAnalyzedAgain(t *testing.T) {
	st := NewMemStore()
	p, _, _ := newPipeline(t, settings(t, policy.PresetStandard, policy.ModeEnforce), members{}, st)
	if _, err := p.Process(t.Context(), msg("M6", "9@lid", "hello all"), t0); err != nil {
		t.Fatal(err)
	}
	edit := msg("M6", "9@lid", "now buy spam")
	edit.IsEdit = true
	v, err := p.Process(t.Context(), edit, t0)
	if err != nil || v.Duplicate || v.Band != BandCertain {
		t.Fatalf("edit verdict %+v, %v; want a fresh certain verdict (edits are re-analyzed, DESIGN §5.1)", v, err)
	}
}

func TestPipelineWithoutPersonAsksInsteadOfBanning(t *testing.T) {
	st := NewMemStore()
	p, rv, _ := newPipeline(t, settings(t, policy.PresetStandard, policy.ModeEnforce), members{}, st)
	if _, err := p.Process(t.Context(), msg("M7", "anon@lid", "buy spam now"), t0); err != nil {
		t.Fatal(err)
	}
	_, bans, acts := st.Snapshot()
	if len(bans) != 0 || !slices.Equal(kinds(acts), []actions.Kind{actions.KindRevoke}) {
		t.Fatalf("bans %d, actions %v: with no person record the spam is deleted but nobody is banned", len(bans), kinds(acts))
	}
	if len(rv.opened) != 1 || rv.opened[0].Kind != review.KindBanDecision || rv.opened[0].Proposed != review.DecisionBan {
		t.Fatalf("reviews %+v: the ban must go to the admins instead", rv.opened)
	}
}

// TestPipelineRailsCountOncePerMessage: under Standard's cap of 10 bans an hour, ten new
// spammers are all banned (each ban counts once), and redeliveries of a handled message spend
// no rail budget at all (DESIGN.md §8.6).
func TestPipelineRailsCountOncePerMessage(t *testing.T) {
	st := NewMemStore()
	p, _, _ := newPipeline(t, settings(t, policy.PresetStandard, policy.ModeEnforce), members{}, st)
	spam := func(i int) *envelope.Envelope {
		e := msg(wa.MessageID(fmt.Sprintf("C%d", i)), wa.JID(fmt.Sprintf("%d@lid", 900+i)), "buy spam now")
		return e
	}
	for range 15 { // the first message, redelivered: only the first counts
		if _, err := p.Process(t.Context(), spam(0), t0); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i < 10; i++ {
		if _, err := p.Process(t.Context(), spam(i), t0); err != nil {
			t.Fatal(err)
		}
	}
	_, _, acts := st.Snapshot()
	kicks := 0
	for _, a := range acts {
		if a.Kind == actions.KindKick {
			kicks++
		}
	}
	if kicks != 10 {
		t.Fatalf("%d kicks for 10 spammers under a cap of 10/hour; want 10 (no double counting, no budget spent on redeliveries)", kicks)
	}
}
