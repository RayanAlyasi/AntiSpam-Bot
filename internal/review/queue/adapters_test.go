package queue_test

import (
	"context"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/safety"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/queue"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

type groupsState map[wa.JID]bool

func (g groupsState) Posting(_ context.Context, chat wa.JID) (actions.Posting, error) {
	return actions.Posting{Protected: g[chat], Silent: g[chat]}, nil
}

func (groupsState) NotAdmin(context.Context, wa.JID, wa.JID) error { return nil }

type metRecord struct{}

func (metRecord) AIMet() bool { return true }

func TestSmallAdapters(t *testing.T) {
	ctx := context.Background()
	prot := queue.ProtectedFrom(groupsState{group: true})
	if p, _ := prot(ctx, group); !p {
		t.Error("ProtectedFrom: a protected group reported unprotected")
	}
	if p, _ := prot(ctx, rgroup); p {
		t.Error("ProtectedFrom: a review group reported protected")
	}

	if ok, _ := (chat.SafetyTrack{Record: safety.Unmet{}}).AIReady(ctx, group); ok {
		t.Error("SafetyTrack with v0.1's Unmet record: want AI not ready (DESIGN §9.2)")
	}
	if ok, _ := (chat.SafetyTrack{}).AIReady(ctx, group); ok {
		t.Error("SafetyTrack without a record: want AI not ready")
	}
	if ok, _ := (chat.SafetyTrack{Record: metRecord{}}).AIReady(ctx, group); !ok {
		t.Error("SafetyTrack with a met record: want AI ready")
	}

	docs := &review.MemoryDocs{}
	s, _ := docs.Load(ctx, group)
	s.Enforcement.Mode = policy.EnforcementMode(review.ModeAskBeforeAction)
	if err := docs.Save(ctx, group, s, "", nil); err != nil {
		t.Fatal(err)
	}
	gs, err := blacklist.PolicySettings{Docs: docs, DefaultCountry: "SA"}.Blacklist(ctx, group)
	if err != nil || !gs.UseInstance || gs.Mode != review.ModeAskBeforeAction || gs.DefaultCountry != "SA" {
		t.Errorf("blacklist settings = %+v, %v; want instance list on, the group's mode and the default country", gs, err)
	}
}
