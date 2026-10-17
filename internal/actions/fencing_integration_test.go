//go:build integration

package actions_test

import (
	"context"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
)

func TestOddBytesPGStore(t *testing.T) {
	oddBytesSuite(t, actions.NewPGStore(storetest.Pool(t), nil))
}

// TestClaimIsFenced: only the store instance that claimed an action settles it, so an executor
// that lost its lock mid-call can't overwrite the new owner's claim.
func TestClaimIsFenced(t *testing.T) {
	ctx := context.Background()
	url := storetest.URL(t, true)
	open := func() *actions.PGStore {
		pool, err := store.Open(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return actions.NewPGStore(pool, nil)
	}
	old, owner := open(), open()
	a := revoke(group(1), "m1", time.Now(), actions.P1)
	a.Bot = bot
	id := mustEnqueue(ctx, t, old, a)
	mustClaim(ctx, t, old, id)
	// The old executor lost its lock; the new owner recovers the row and claims it.
	if _, err := owner.Recover(ctx, bot); err != nil {
		t.Fatal(err)
	}
	mustClaim(ctx, t, owner, id)
	if err := old.Finish(ctx, id, actions.StatusDone, "", actions.Result{}); err == nil {
		t.Error("the old executor settled the new owner's claim, want an error (fencing)")
	}
	if err := old.Retry(ctx, id, actions.RetryInfo{NotBefore: time.Now()}); err == nil {
		t.Error("the old executor retried the new owner's claim, want an error (fencing)")
	}
	if err := owner.Finish(ctx, id, actions.StatusDone, "", actions.Result{}); err != nil {
		t.Errorf("the owner's Finish = %v, want nil", err)
	}
}
