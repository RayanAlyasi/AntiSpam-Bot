package pipeline

import (
	"fmt"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// shadowReacts returns the shadow-mode reactions among the enqueued actions.
func shadowReacts(as []actions.Action) []actions.Action {
	var out []actions.Action
	for _, a := range as {
		if a.Kind == actions.KindReact && a.Payload.Purpose == actions.PurposeShadow {
			out = append(out, a)
		}
	}
	return out
}

func TestPipelineShadowReactions(t *testing.T) {
	established := members{"2@lid": {Known: true, Member: policy.MemberRecord{LID: "2@lid", Role: policy.RoleMember, JoinedAt: t0.AddDate(-1, 0, 0), PreExisting: true}}}
	tests := []struct {
		name      string
		mode      policy.Mode
		reactions bool
		m         members
		sender    wa.JID
		text      string
		route     Route
		emoji     string // "" for no reaction
	}{
		{"certain spam would be banned", policy.ModeShadow, true, members{}, "9@lid", "buy spam now", RouteAutoBan, ShadowCertain},
		{"established member: admins decide the ban", policy.ModeShadow, true, established, "2@lid", "buy spam now", RouteAsk, ShadowAsk},
		{"still uncertain: admins decide", policy.ModeShadow, true, members{}, "9@lid", "maybe", RouteAsk, ShadowAsk},
		{"clean message", policy.ModeShadow, true, members{}, "9@lid", "hello all", RouteNone, ""},
		{"reactions off", policy.ModeShadow, false, members{}, "9@lid", "buy spam now", RouteAutoBan, ""},
		{"enforce mode", policy.ModeEnforce, true, members{}, "9@lid", "buy spam now", RouteAutoBan, ""},
		{"paused mode", policy.ModePaused, true, members{}, "9@lid", "buy spam now", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := settings(t, policy.PresetStandard, tt.mode)
			s.Shadow.Reactions = tt.reactions
			st := NewMemStore()
			p, _, wakes := newPipeline(t, s, tt.m, st)
			v, err := p.Process(t.Context(), msg("M1", tt.sender, tt.text), t0)
			if err != nil {
				t.Fatal(err)
			}
			if tt.route != "" && v.Route != tt.route {
				t.Fatalf("route %s, want %s (the case doesn't test what its name says)", v.Route, tt.route)
			}
			dets, _, acts := st.Snapshot()
			reacts := shadowReacts(acts)
			if tt.emoji == "" {
				if len(reacts) != 0 {
					t.Fatalf("shadow reactions %+v, want none", reacts)
				}
				return
			}
			if len(reacts) != 1 || len(acts) != 1 {
				t.Fatalf("enqueued %v, want exactly one shadow reaction (shadow mode deletes and bans nothing, DESIGN §8.4)", kinds(acts))
			}
			r := reacts[0]
			want := actions.Payload{Target: wa.MessageRef{Sender: tt.sender, ID: "M1"}, Emoji: tt.emoji, Purpose: actions.PurposeShadow}
			if r.Payload.Target != want.Target || r.Payload.Emoji != want.Emoji || r.Priority != actions.P4 ||
				r.DedupeKey != actions.ShadowReactKey(group, "M1") || r.Chat != group || r.Bot != bot {
				t.Fatalf("reaction %+v, want %+v at P4 on the message, keyed per message", r, want)
			}
			if len(dets) != 1 || r.ReasonDetectionID != dets[0].ID || *wakes != 1 {
				t.Fatalf("detections %d, reason %d, wakes %d; want the reaction linked to its detection and the executor woken", len(dets), r.ReasonDetectionID, *wakes)
			}
		})
	}
}

func TestPipelineShadowReactsOncePerMessage(t *testing.T) {
	s := settings(t, policy.PresetStandard, policy.ModeShadow)
	s.Shadow.Reactions = true
	st := NewMemStore()
	p, _, _ := newPipeline(t, s, members{}, st)
	for i, text := range []string{"maybe", "now buy spam", "buy spam again"} {
		e := msg("M7", "9@lid", text)
		e.IsEdit = i > 0
		if _, err := p.Process(t.Context(), e, t0); err != nil {
			t.Fatal(err)
		}
	}
	_, _, acts := st.Snapshot()
	if r := shadowReacts(acts); len(r) != 1 || r[0].Payload.Emoji != ShadowAsk {
		t.Fatalf("shadow reactions %+v, want only the first one (❓): edits never react again", r)
	}
}

func TestPipelineNoShadowReactionsWhenRailsDemoteEnforce(t *testing.T) {
	s := settings(t, policy.PresetStandard, policy.ModeEnforce)
	s.Shadow.Reactions = true // left on after shadow testing
	s.Limits.MaxDeletionsPerMinute = 1
	st := NewMemStore()
	p, _, _ := newPipeline(t, s, members{}, st)
	for i := range 3 {
		e := msg(wa.MessageID(fmt.Sprintf("R%d", i)), wa.JID(fmt.Sprintf("%d@lid", 900+i)), "buy spam now")
		if _, err := p.Process(t.Context(), e, t0); err != nil {
			t.Fatal(err)
		}
	}
	dets, _, acts := st.Snapshot()
	if dets[len(dets)-1].Mode != policy.DetectionShadow {
		t.Fatalf("last detection mode %s, want shadow (the deletion circuit must have opened for this test to mean anything)", dets[len(dets)-1].Mode)
	}
	if r := shadowReacts(acts); len(r) != 0 {
		t.Fatalf("enforce group enqueued %d shadow reactions after the circuit opened, want none (only shadow mode reacts)", len(r))
	}
}
