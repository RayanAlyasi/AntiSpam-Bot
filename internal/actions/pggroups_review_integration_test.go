//go:build integration

package actions_test

import (
	"context"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestPGGroupsReviewGroupWithOldSettings: a group that was protected keeps its settings document
// after the bot is demoted there; once it is named as a review group, the bot posts there,
// unless it is an admin moderating the group again (DESIGN.md §9.5, §9.7).
func TestPGGroupsReviewGroupWithOldSettings(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	docs := map[wa.JID]string{
		group(1): `{"silent": true, "review": {"review_group": "12030005@g.us"}}`,
		group(2): `{"silent": true, "review": {"review_group": "12030006@g.us"}}`,
		group(3): `{"silent": true, "review": {"review_group": "12030007@g.us"}}`,
		group(5): `{"silent": true}`,
		group(6): `{"silent": true}`,
		group(7): `{"silent": true}`,
		group(8): `{"silent": true}`,
		group(9): `{"silent": true, "review": {"review_group": "12030009@g.us"}}`,
	}
	for jid, doc := range docs {
		if _, err := pool.Exec(ctx, `INSERT INTO app.group_settings (group_jid, version, doc, updated_by) VALUES ($1, 1, $2, 'test')`, string(jid), doc); err != nil {
			t.Fatal(err)
		}
	}
	for jid, status := range map[wa.JID]string{
		group(5): "pending_admin", group(6): "active", group(7): "left", group(8): "pending_admin",
		group(9): "pending_admin",
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO app.groups (jid, status) VALUES ($1, $2)`, string(jid), status); err != nil {
			t.Fatal(err)
		}
	}
	g := actions.PGGroups{DB: pool}
	cases := []struct {
		chat wa.JID
		want actions.Posting
		why  string
	}{
		{group(5), actions.Posting{}, "demoted, then made a review group"},
		{group(6), actions.Posting{Protected: true, Silent: true}, "a review group where the bot is admin again is protected"},
		{group(7), actions.Posting{}, "a review group the bot left isn't protected"},
		{group(8), actions.Posting{Protected: true, Silent: true}, "demoted but no review group: still silent"},
		{group(9), actions.Posting{Protected: true, Silent: true}, "a group can't be its own review group"},
	}
	for _, tc := range cases {
		got, err := g.Posting(ctx, tc.chat)
		if err != nil || got != tc.want {
			t.Errorf("Posting(%s) = %+v, %v; want %+v (%s)", tc.chat, got, err, tc.want, tc.why)
		}
	}
}
