package main

import (
	"context"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestMarkNotAdminStopsModeration: a group whose bot lost admin goes back to pending_admin, so
// routing stops treating it as moderated; a group the bot left stays left.
func TestMarkNotAdminStopsModeration(t *testing.T) {
	ctx := context.Background()
	groups := identity.NewMemoryGroupStore()
	active, left := wa.JID("120300001@g.us"), wa.JID("120300002@g.us")
	for _, g := range []identity.Group{
		{JID: active, BotIsAdmin: true, Status: identity.GroupActive},
		{JID: left, Status: identity.GroupLeft},
	} {
		if err := groups.UpsertGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	mark := markNotAdmin(groups)
	for _, g := range []wa.JID{active, left} {
		if err := mark(ctx, "", g); err != nil {
			t.Fatal(err)
		}
	}
	if g, _, _ := groups.Group(ctx, active); g.BotIsAdmin || g.Status != identity.GroupPendingAdmin {
		t.Errorf("after a not-admin error: admin %v, status %q; want false and pending_admin", g.BotIsAdmin, g.Status)
	}
	if g, _, _ := groups.Group(ctx, left); g.Status != identity.GroupLeft {
		t.Errorf("a left group became %q, want it left alone", g.Status)
	}
}
