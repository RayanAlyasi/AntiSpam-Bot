package pipeline

import (
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestPipelineShadowReactionTakesTheWouldBePriority: a shadow reaction stands in for what enforce
// mode would have done, so it is queued at that action's priority (DESIGN.md §8.4).
func TestPipelineShadowReactionTakesTheWouldBePriority(t *testing.T) {
	established := members{"2@lid": {Known: true, Member: policy.MemberRecord{LID: "2@lid", Role: policy.RoleMember, JoinedAt: t0.AddDate(-1, 0, 0), PreExisting: true}}}
	tests := []struct {
		name   string
		m      members
		sender wa.JID
		text   string
		want   actions.Priority
	}{
		{"certain spam: the delete's P1", members{}, "9@lid", "buy spam now", actions.P1},
		{"established member: still deleted, so P1", established, "2@lid", "buy spam now", actions.P1},
		{"uncertain: only the review, P3", members{}, "9@lid", "maybe", actions.P3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := settings(t, policy.PresetStandard, policy.ModeShadow)
			s.Shadow.Reactions = true
			st := NewMemStore()
			p, _, _ := newPipeline(t, s, tt.m, st)
			if _, err := p.Process(t.Context(), msg("M1", tt.sender, tt.text), t0); err != nil {
				t.Fatal(err)
			}
			_, _, acts := st.Snapshot()
			r := shadowReacts(acts)
			if len(r) != 1 || r[0].Priority != tt.want {
				t.Fatalf("shadow reactions %+v, want one at %s (the priority of the action it stands in for)", r, tt.want)
			}
		})
	}
}

func TestShadowPriority(t *testing.T) {
	tests := []struct {
		name  string
		would []policy.Action
		want  actions.Priority
	}{
		{"delete, ban and review", []policy.Action{{Kind: policy.ActionDelete, Priority: policy.P1}, {Kind: policy.ActionBan, Priority: policy.P2}, {Kind: policy.ActionReview, Priority: policy.P3}}, actions.P1},
		{"review only", []policy.Action{{Kind: policy.ActionReview, Priority: policy.P3}}, actions.P3},
		{"nothing", nil, actions.P4},
		{"out of range ignored", []policy.Action{{Kind: policy.ActionReview, Priority: 9}}, actions.P4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shadowPriority(Decision{Plan: policy.Plan{Would: tt.would}}); got != tt.want {
				t.Fatalf("shadowPriority = %s, want %s", got, tt.want)
			}
		})
	}
}
