//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
)

func TestAuditLogInstanceEntries(t *testing.T) {
	pool := storetest.Pool(t)
	ctx := context.Background()
	for _, e := range []store.AuditEntry{
		{Actor: store.ActorOperator, Action: "instance.pacing"},
		{Actor: store.ActorSystem, GroupJID: "g@g.us", Action: "action.ban"},
		{Actor: store.ActorOperator, Action: "instance.ai"},
	} {
		if _, err := store.WriteAudit(ctx, pool, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.ListAudit(ctx, pool, "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Action != "instance.ai" || got[1].Action != "instance.pacing" {
		t.Errorf("instance log = %+v, want [instance.ai instance.pacing] (entries without a group must be readable)", got)
	}
}
