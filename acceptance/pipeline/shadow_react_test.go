package pipeline_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const (
	srBot   wa.JID = "100@lid"
	srGroup wa.JID = "120363000000000501@g.us"

	srNewcomer    wa.JID = "301@lid"
	srEstablished wa.JID = "302@lid"
	srAdmin       wa.JID = "303@lid"

	srWouldAct = "❌" // ❌
	srWouldAsk = "❓" // ❓
)

var srT0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// srWords flags "spam" as known spam (CERTAIN) and "maybe" as a lone heuristic (UNCERTAIN, so with
// AI off it goes to admins).
type srWords struct{}

func (srWords) Name() string                                      { return "sr_words" }
func (srWords) Tier() detect.Tier                                 { return detect.T0 }
func (srWords) Applies(*envelope.Envelope, *policy.Settings) bool { return true }
func (srWords) Analyze(_ context.Context, e *envelope.Envelope, _ *detect.State) ([]detect.Signal, error) {
	var out []detect.Signal
	for _, t := range e.Texts {
		switch {
		case strings.Contains(t.Norm, "spam"):
			out = append(out, detect.Signal{Code: "test.known", Weight: 5, Known: true, Evidence: "known phrase"})
		case strings.Contains(t.Norm, "maybe"):
			out = append(out, detect.Signal{Code: "test.heuristic", Weight: 4, Evidence: "heuristic"})
		}
	}
	return out, nil
}

type srGroups policy.Settings

func (g srGroups) Settings(_ context.Context, chat wa.JID) (policy.Settings, bool, error) {
	return policy.Settings(g), chat == srGroup, nil
}

type srMembers struct{}

func (srMembers) Sender(_ context.Context, _ wa.JID, who envelope.Identity) (pipeline.Sender, int64, error) {
	old := policy.MemberRecord{LID: string(who.LID), Role: policy.RoleMember, JoinedAt: srT0.AddDate(-1, 0, 0), PreExisting: true}
	switch who.LID {
	case srEstablished:
		return pipeline.Sender{Known: true, Member: old}, 2, nil
	case srAdmin:
		old.Role = policy.RoleAdmin
		return pipeline.Sender{Known: true, Member: old}, 3, nil
	}
	return pipeline.Sender{}, 1, nil // never seen before: a new member
}

func srSettings(t *testing.T, p policy.Preset, mode policy.Mode, reactions bool) policy.Settings {
	t.Helper()
	s, err := policy.PresetSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	s.Mode = mode
	s.Shadow.Reactions = reactions
	return s
}

func srPipeline(t *testing.T, s policy.Settings) (*pipeline.Pipeline, *pipeline.MemStore) {
	t.Helper()
	en, err := pipeline.NewEngine(pipeline.EngineConfig{
		Analyzers: []pipeline.Analyzer{srWords{}}, Weights: scoring.DefaultWeights(),
		Normalize: pipeline.NormalizeTexts, Clock: clock.NewFake(srT0),
	})
	if err != nil {
		t.Fatal(err)
	}
	st := pipeline.NewMemStore()
	p, err := pipeline.New(pipeline.Config{
		Engine: en, Groups: srGroups(s), Members: srMembers{}, Store: st,
		Bot: srBot, Clock: clock.NewFake(srT0), RuleVersion: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return p, st
}

func srMsg(id wa.MessageID, sender wa.JID, kind envelope.Kind, text string) *envelope.Envelope {
	return &envelope.Envelope{
		Bot: srBot, Group: srGroup, Sender: envelope.Identity{LID: sender}, MsgID: id, Kind: kind,
		Texts: []envelope.TextSurface{{Source: "body", Raw: text}},
	}
}

func srReacts(acts []actions.Action) []actions.Action {
	var out []actions.Action
	for _, a := range acts {
		if a.Kind == actions.KindReact {
			out = append(out, a)
		}
	}
	return out
}

func TestShadowReactionFollowsRoute(t *testing.T) {
	blockPolls := func(s *policy.Settings) { s.Content.Polls = policy.RuleBlock }
	tests := []struct {
		name      string
		preset    policy.Preset
		mode      policy.Mode
		reactions bool
		tweak     func(*policy.Settings)
		sender    wa.JID
		kind      envelope.Kind
		text      string
		route     pipeline.Route // "": not checked
		emoji     string         // "": no reaction
		prio      actions.Priority
		why       string
	}{
		{"auto ban", policy.PresetStandard, policy.ModeShadow, true, nil, srNewcomer, envelope.KindText, "buy spam now", pipeline.RouteAutoBan, srWouldAct, actions.P1, "auto_ban acts without asking"},
		{"delete only", policy.PresetRelaxed, policy.ModeShadow, true, nil, srNewcomer, envelope.KindText, "buy spam now", pipeline.RouteDeleteOnly, srWouldAct, actions.P1, "delete_only acts without asking"},
		{"policy violation", policy.PresetStandard, policy.ModeShadow, true, blockPolls, srNewcomer, envelope.KindPoll, "lunch on friday?", pipeline.RoutePolicy, srWouldAct, actions.P1, "a content-rule delete acts without asking"},
		{"established member asks", policy.PresetStandard, policy.ModeShadow, true, nil, srEstablished, envelope.KindText, "buy spam now", pipeline.RouteAsk, srWouldAsk, actions.P1, "the ban of an established member goes to admins"},
		{"ask admins preset", policy.PresetAskAdmins, policy.ModeShadow, true, nil, srNewcomer, envelope.KindText, "buy spam now", pipeline.RouteAsk, srWouldAsk, actions.P1, "ask-admins mode asks"},
		{"uncertain asks", policy.PresetStandard, policy.ModeShadow, true, nil, srNewcomer, envelope.KindText, "maybe this", pipeline.RouteAsk, srWouldAsk, actions.P3, "still uncertain goes to admins"},
		{"clean message", policy.PresetStandard, policy.ModeShadow, true, nil, srNewcomer, envelope.KindText, "see you at five", "", "", 0, "a clean message wasn't caught"},
		{"admin sender", policy.PresetStandard, policy.ModeShadow, true, nil, srAdmin, envelope.KindText, "buy spam now", "", "", 0, "admins are never acted on, so nothing was caught"},
		{"reactions off", policy.PresetStandard, policy.ModeShadow, false, nil, srNewcomer, envelope.KindText, "buy spam now", pipeline.RouteAutoBan, "", 0, "shadow.reactions is opt-in"},
		{"enforce mode", policy.PresetStandard, policy.ModeEnforce, true, nil, srNewcomer, envelope.KindText, "buy spam now", pipeline.RouteAutoBan, "", 0, "only shadow mode reacts"},
		{"paused mode", policy.PresetStandard, policy.ModePaused, true, nil, srNewcomer, envelope.KindText, "buy spam now", "", "", 0, "paused acts on nothing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := srSettings(t, tt.preset, tt.mode, tt.reactions)
			if tt.tweak != nil {
				tt.tweak(&s)
			}
			p, st := srPipeline(t, s)
			const id wa.MessageID = "3EB0SHADOW01"
			v, err := p.Process(t.Context(), srMsg(id, tt.sender, tt.kind, tt.text), srT0)
			if err != nil {
				t.Fatalf("Process: %v, want nil", err)
			}
			if tt.route != "" && v.Route != tt.route {
				t.Fatalf("got route %q, want %q (test setup: %s)", v.Route, tt.route, tt.why)
			}
			_, _, acts := st.Snapshot()
			reacts := srReacts(acts)
			if tt.emoji == "" {
				if len(reacts) != 0 {
					t.Fatalf("enqueued %d react actions (emoji %q), want none (%s, DESIGN §8.4)", len(reacts), reacts[0].Payload.Emoji, tt.why)
				}
				if tt.mode == policy.ModeShadow && len(acts) != 0 {
					t.Errorf("shadow mode enqueued %d actions, want none (shadow acts on nothing, DESIGN §8.4)", len(acts))
				}
				return
			}
			if len(acts) != 1 || len(reacts) != 1 {
				t.Fatalf("enqueued %d actions (%d reacts), want exactly one react and nothing else (shadow mode only shows its judgement, DESIGN §8.4)", len(acts), len(reacts))
			}
			a := reacts[0]
			if a.Payload.Emoji != tt.emoji {
				t.Errorf("got emoji %q, want %q (%s: ❌ would act, ❓ would ask, DESIGN §8.4)", a.Payload.Emoji, tt.emoji, tt.why)
			}
			if a.Payload.Purpose != actions.PurposeShadow {
				t.Errorf("got purpose %q, want %q (only shadow reactions may post in a silent group, DESIGN §9.7)", a.Payload.Purpose, actions.PurposeShadow)
			}
			if a.Priority != tt.prio {
				t.Errorf("got priority %s, want %s (a shadow reaction takes the priority of the action it stands in for: P1 for a delete, P3 when only a review would run, DESIGN §8.4)", a.Priority, tt.prio)
			}
			if want := (wa.MessageRef{Sender: tt.sender, ID: id}); a.Payload.Target != want {
				t.Errorf("got target %+v, want %+v (the reaction goes on the caught message)", a.Payload.Target, want)
			}
			if a.Chat != srGroup || a.Bot != srBot {
				t.Errorf("got chat %s bot %s, want %s and %s", a.Chat, a.Bot, srGroup, srBot)
			}
			if want := actions.ShadowReactKey(srGroup, id); a.DedupeKey != want {
				t.Errorf("got dedupe key %q, want %q (one reaction per message, DESIGN §10.4)", a.DedupeKey, want)
			}
			if a.ReasonDetectionID == 0 || a.ReasonDetectionID != v.DetectionID {
				t.Errorf("got reason detection %d, want the shadow detection %d (every action links to its detection, DESIGN §8.6)", a.ReasonDetectionID, v.DetectionID)
			}
		})
	}
}

func TestShadowReactionOncePerMessageAcrossEdits(t *testing.T) {
	p, st := srPipeline(t, srSettings(t, policy.PresetStandard, policy.ModeShadow, true))
	const id wa.MessageID = "3EB0SHADOW02"

	v, err := p.Process(t.Context(), srMsg(id, srNewcomer, envelope.KindText, "maybe this"), srT0)
	if err != nil || v.Route != pipeline.RouteAsk {
		t.Fatalf("original: route %q, err %v; want ask (test setup: a lone heuristic goes to admins)", v.Route, err)
	}
	edit := srMsg(id, srNewcomer, envelope.KindText, "now buy spam")
	edit.IsEdit = true
	v, err = p.Process(t.Context(), edit, srT0.Add(time.Minute))
	if err != nil || v.Route != pipeline.RouteAutoBan {
		t.Fatalf("edit: route %q, err %v; want auto_ban (test setup: the edit is judged differently)", v.Route, err)
	}
	if _, err := p.Process(t.Context(), edit, srT0.Add(time.Minute)); err != nil {
		t.Fatalf("redelivered edit: %v, want nil", err)
	}

	_, _, acts := st.Snapshot()
	reacts := srReacts(acts)
	if len(reacts) != 1 {
		t.Fatalf("got %d react actions for one message, want 1 (one reaction per message, not per edit or redelivery, DESIGN §8.4, §10.4)", len(reacts))
	}
	if reacts[0].Payload.Emoji != srWouldAsk {
		t.Errorf("got emoji %q, want %q (the first judgement's reaction stands; an edit adds none)", reacts[0].Payload.Emoji, srWouldAsk)
	}
	if len(acts) != 1 {
		t.Errorf("got %d actions, want just the one react (shadow acts on nothing, DESIGN §8.4)", len(acts))
	}
}
