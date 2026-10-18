//go:build integration

package actions_test

import (
	"context"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestPGGroupsShadowReactions: shadow reactions are allowed only for a group in shadow mode with
// shadow.reactions on; a document written before the key existed counts as off (DESIGN.md §8.4).
func TestPGGroupsShadowReactions(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	cases := []struct {
		doc  string
		want actions.Posting
		why  string
	}{
		{`{"mode": "shadow", "silent": true, "shadow": {"reactions": true}}`, actions.Posting{Protected: true, Silent: true, ShadowReactions: true}, "shadow mode, reactions on"},
		{`{"mode": "shadow", "silent": true, "shadow": {"reactions": false}}`, actions.Posting{Protected: true, Silent: true}, "reactions off"},
		{`{"mode": "shadow", "silent": true}`, actions.Posting{Protected: true, Silent: true}, "no shadow key: off by default"},
		{`{"mode": "enforce", "silent": true, "shadow": {"reactions": true}}`, actions.Posting{Protected: true, Silent: true}, "enforce mode never shows shadow reactions"},
		{`{"silent": false, "shadow": {"reactions": true}}`, actions.Posting{Protected: true}, "no mode key: not shadow"},
	}
	g := actions.PGGroups{DB: pool}
	for i, tc := range cases {
		jid := group(10 + i)
		if _, err := pool.Exec(ctx, `INSERT INTO app.group_settings (group_jid, version, doc, updated_by) VALUES ($1, 1, $2, 'test')`, string(jid), tc.doc); err != nil {
			t.Fatal(err)
		}
		if got, err := g.Posting(ctx, jid); err != nil || got != tc.want {
			t.Errorf("Posting(%s) = %+v, %v; want %+v (%s)", tc.doc, got, err, tc.want, tc.why)
		}
	}
	if got, err := g.Posting(ctx, wa.JID("120399990000@g.us")); err != nil || got.ShadowReactions {
		t.Errorf("unknown group: %+v, %v; want no shadow reactions", got, err)
	}
}
