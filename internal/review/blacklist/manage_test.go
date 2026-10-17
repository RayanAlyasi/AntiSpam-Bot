package blacklist_test

import (
	"context"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist/blacklisttest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestInstanceRemoveLiftsOnlyItsOwnBlocks: removing an instance entry unbans only where the
// blacklist itself could have banned, and only blacklist blocks (DESIGN §9.8 Undo).
func TestInstanceRemoveLiftsOnlyItsOwnBlocks(t *testing.T) {
	ctx := context.Background()
	const (
		g1, g2, g3 wa.JID = "1@g.us", "2@g.us", "3@g.us"
		op         wa.JID = "9@lid"
	)
	groups := &blacklisttest.Groups{}
	for _, g := range []wa.JID{g1, g2, g3} {
		groups.SetMembers(g)
	}
	settings := &blacklisttest.Settings{Default: blacklist.GroupSettings{UseInstance: true, Mode: review.ModeAuto}}
	settings.Set(g2, blacklist.GroupSettings{UseInstance: false, Mode: review.ModeAuto})
	settings.Set(g3, blacklist.GroupSettings{UseInstance: true, Mode: review.ModeAskDeleteFirst})
	acts := &reviewtest.Actions{}
	svc := blacklist.NewService(blacklist.Deps{List: blacklist.NewMemoryList(), Groups: groups, Settings: settings, Actions: acts})
	if _, err := svc.Add(ctx, blacklist.AddRequest{Numbers: []string{"+966551234567"}, By: op, Operator: true, Instance: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Remove(ctx, blacklist.RemoveRequest{Numbers: []string{"+966551234567"}, By: op, Operator: true, Instance: true}); err != nil {
		t.Fatal(err)
	}
	unbans := reviewtest.Of[review.Unban](acts)
	if len(unbans) != 1 || unbans[0].Group != g1 || unbans[0].Via != "blacklist" {
		t.Errorf("unbans = %+v, want one blacklist-scoped unban in %s only (opted-out and ask groups never banned)", unbans, g1)
	}
}
