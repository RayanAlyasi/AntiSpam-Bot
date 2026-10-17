//go:build integration

package actions_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestPGGroupsPostingRules: posting rules come from the group settings documents; anything the
// bot can't place is treated as a silent protected group (DESIGN.md §9.7).
func TestPGGroupsPostingRules(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	for jid, doc := range map[wa.JID]string{
		group(1): `{"silent": true, "review": {"review_group": "120300009999@g.us"}}`,
		group(2): `{"silent": false}`,
		group(3): `{}`,
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO app.group_settings (group_jid, version, doc, updated_by) VALUES ($1, 1, $2, 'test')`, string(jid), doc); err != nil {
			t.Fatal(err)
		}
	}
	g := actions.PGGroups{DB: pool}
	cases := []struct {
		chat wa.JID
		want actions.Posting
		why  string
	}{
		{group(1), actions.Posting{Protected: true, Silent: true}, "silent protected group"},
		{group(2), actions.Posting{Protected: true}, "protected group with silent off"},
		{group(3), actions.Posting{Protected: true, Silent: true}, "no silent key: silent by default"},
		{"120300009999@g.us", actions.Posting{}, "a review group isn't protected"},
		{group(77), actions.Posting{Protected: true, Silent: true}, "an unknown group gets no posts"},
		{"700@s.whatsapp.net", actions.Posting{}, "a DM is never protected"},
	}
	for _, tc := range cases {
		got, err := g.Posting(ctx, tc.chat)
		if err != nil || got != tc.want {
			t.Errorf("Posting(%s) = %+v, %v; want %+v (%s)", tc.chat, got, err, tc.want, tc.why)
		}
	}
}

// TestExecutorRefusesPostsInSilentGroupOnPostgres: with the real settings documents, no send,
// edit or reaction reaches a silent protected group, while its review group gets its post.
func TestExecutorRefusesPostsInSilentGroupOnPostgres(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	reviewGroup := wa.JID("120300009999@g.us")
	if _, err := pool.Exec(ctx, `INSERT INTO app.group_settings (group_jid, version, doc, updated_by) VALUES ($1, 1, $2, 'test')`,
		string(group(1)), `{"silent": true, "review": {"review_group": "120300009999@g.us"}}`); err != nil {
		t.Fatal(err)
	}
	s := actions.NewPGStore(pool, nil)
	client := newTimed()
	cfg := actions.DefaultConfig()
	cfg.Spacing, cfg.Jitter, cfg.PollInterval = time.Millisecond, time.Millisecond, 20*time.Millisecond
	ex, err := actions.NewExecutor(cfg, actions.Deps{Bot: bot, Client: client, Store: s, Groups: actions.PGGroups{DB: pool}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i, k := range actions.Kinds {
		if !k.Posts() {
			continue
		}
		a := actions.Action{
			Bot: bot, Chat: group(1), Kind: k, Priority: actions.P0, DedupeKey: fmt.Sprint("post", i),
			Payload: actions.Payload{Text: "hi", Target: wa.MessageRef{ID: "x", Sender: member(1)}, Emoji: "👍"},
		}
		ids = append(ids, mustEnqueue(ctx, t, s, a))
	}
	review := send(reviewGroup, "review", "Spam? ✅/❌", actions.PurposeReview)
	review.Bot = bot
	reviewID := mustEnqueue(ctx, t, s, review)
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := ex.Run(rctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if got := mustGet(ctx, t, s, id).Status; got != actions.StatusRefused {
			t.Errorf("post %d in a silent protected group is %s, want refused", id, got)
		}
	}
	for _, c := range client.writes() {
		if c.chat == group(1) {
			t.Errorf("WhatsApp got %s in the silent group, want nothing posted there", c.method)
		}
	}
	if got := mustGet(ctx, t, s, reviewID).Status; got != actions.StatusDone {
		t.Errorf("review post in the review group is %s, want done", got)
	}
}
