//go:build integration

package pgstore_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
)

// TestMessageTextLifecycle: the corpus gets the unredacted text once, at the decision; a later
// overturn removes the text from the review row as well as the snapshot; and review evidence
// expires with the 30-day retention (DESIGN §7.5, §14, §17.1).
func TestMessageTextLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	c := clock.NewFake(start)
	st, ev, corpus, admins := pgstore.Reviews{DB: pool}, pgstore.Evidence{DB: pool}, &reviewtest.Corpus{}, &reviewtest.Admins{}
	learning := review.NewLearning(review.LearningDeps{Evidence: ev, Corpus: corpus, Standing: &reviewtest.Standing{}, Clock: c})
	svc := review.NewService(review.Deps{Store: st, Settings: &reviewtest.Settings{}, Admins: admins, Actions: &reviewtest.Actions{}, Learner: learning, Clock: c})
	admins.Set(group, "1@lid", true)

	it, err := svc.Open(ctx, item())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Vote(ctx, review.Ballot{ReviewID: it.ID, Voter: "1@lid", Decision: review.DecisionBan}); err != nil {
		t.Fatal(err)
	}
	if a := corpus.Active(); len(a) != 1 || !strings.Contains(a[0].Text, "123 4567") {
		t.Fatalf("corpus = %+v, want the unredacted text so advertised numbers can be matched (DESIGN §7.5)", a)
	}
	var raw *string
	if err := pool.QueryRow(ctx, "SELECT evidence_raw FROM app.reviews WHERE id = $1", it.ID).Scan(&raw); err != nil || raw != nil {
		t.Fatalf("evidence_raw after the decision = %v, %v; want it cleared", raw, err)
	}

	if err := learning.Overturn(ctx, review.Overturn{Group: group, Message: *it.Message, Actor: "1@lid"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(ctx, it.ID)
	if err != nil || got.Evidence.Text != "" || got.Evidence.Thumbnail != nil {
		t.Fatalf("review after the overturn: text %q thumbnail %v, %v; want no text of the message left (DESIGN §7.5)", got.Evidence.Text, got.Evidence.Thumbnail != nil, err)
	}

	second := item()
	second.Message.ID = "SPAM2"
	it2, err := svc.Open(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ev.Purge(ctx, start.Add(review.EvidenceRetention+time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Get(ctx, it2.ID)
	if err := pool.QueryRow(ctx, "SELECT evidence_raw FROM app.reviews WHERE id = $1", it2.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if got.Evidence.Text != "" || got.Evidence.Thumbnail != nil || raw != nil {
		t.Errorf("review evidence after 30 days: text %q thumbnail %v raw %v; want it expired (DESIGN §14)", got.Evidence.Text, got.Evidence.Thumbnail != nil, raw)
	}
}
