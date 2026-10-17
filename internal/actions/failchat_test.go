package actions_test

import (
	"context"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
)

// failChatSuite: FailChat fails exactly the chat's pending actions of the given kinds and
// returns them, so the executor can report each one.
func failChatSuite(t *testing.T, s actions.Store) {
	ctx := context.Background()
	add := func(a actions.Action) int64 {
		a.Bot = bot
		return mustEnqueue(ctx, t, s, a)
	}
	del := add(revoke(group(1), "m1", time.Now(), actions.P1))
	k := add(kick(group(1), member(1)))
	other := add(revoke(group(2), "m2", time.Now(), actions.P1))
	dm := add(send("700@s.whatsapp.net", "dm", "hi", actions.PurposeDM))
	failed, err := s.FailChat(ctx, bot, group(1), []actions.Kind{actions.KindRevoke, actions.KindKick}, "not admin")
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 2 || failed[0].ID != del || failed[1].ID != k || failed[0].Status != actions.StatusFailed {
		t.Errorf("FailChat returned %+v, want the group's delete and kick, failed", failed)
	}
	for _, id := range []int64{other, dm} {
		if got := mustGet(ctx, t, s, id).Status; got != actions.StatusPending {
			t.Errorf("action %d outside the chat or kinds is %s, want pending", id, got)
		}
	}
}

func TestFailChatMemStore(t *testing.T) { failChatSuite(t, actions.NewMemStore(nil)) }
