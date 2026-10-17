//go:build integration

package pgstore_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const (
	group  wa.JID = "120363000000000001@g.us"
	rgroup wa.JID = "120363000000000099@g.us"
	ahmed  wa.JID = "200000000000001@lid"
)

var start = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func item() review.NewItem {
	return review.NewItem{
		Group: group, GroupName: "Cars", Kind: review.KindBanDecision,
		Subject:  review.Subject{Person: ahmed, Name: "Ahmed", Established: true, MemberFor: 48 * time.Hour},
		Message:  &wa.MessageRef{Sender: ahmed, ID: "SPAM1"},
		Evidence: review.Evidence{Text: "call +966 55 123 4567", Score: 0.9, Source: "rules", MessageDeleted: true, Thumbnail: &wa.Image{Data: []byte{1}, Mimetype: "image/jpeg"}},
	}
}

// TestFirstVoteWinsOnPostgres: the conditional UPDATE lets exactly one of many concurrent
// votes decide, and every vote is stored (DESIGN §9.5).
func TestFirstVoteWinsOnPostgres(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	st := pgstore.Reviews{DB: pool}
	acts, admins := &reviewtest.Actions{}, &reviewtest.Admins{}
	svc := review.NewService(review.Deps{
		Store: st, Settings: &reviewtest.Settings{Default: review.Config{Destination: review.DestinationGroup, ReviewGroup: rgroup}},
		Admins: admins, Actions: acts, Clock: clock.NewFake(start),
	})
	it, err := svc.Open(ctx, item())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordPosted(ctx, review.Posted{ReviewID: it.ID, Chat: rgroup, ID: "BOT1", SentAt: start}); err != nil {
		t.Fatal(err)
	}
	const voters = 16
	var wg sync.WaitGroup
	results := make(chan review.Outcome, voters)
	for i := range voters {
		v := wa.JID(fmt.Sprintf("30000000000%04d@lid", i))
		admins.Set(group, v, true)
		wg.Go(func() {
			res, err := svc.Vote(ctx, review.Ballot{ReviewID: it.ID, Voter: v, Decision: review.DecisionBan, Method: review.MethodReaction})
			if err != nil {
				t.Error(err)
			}
			results <- res.Outcome
		})
	}
	wg.Wait()
	close(results)
	won := 0
	for o := range results {
		if o == review.OutcomeDecided {
			won++
		}
	}
	if won != 1 || len(reviewtest.Of[review.Ban](acts)) != 1 {
		t.Fatalf("%d votes decided and %d bans, want exactly one of each (first-wins)", won, len(reviewtest.Of[review.Ban](acts)))
	}
	votes, err := st.Votes(ctx, it.ID)
	if err != nil || len(votes) != voters {
		t.Fatalf("stored %d votes, %v; want all %d", len(votes), err, voters)
	}
	got, err := st.ByMessage(ctx, rgroup, "BOT1")
	if err != nil || got.ID != it.ID || got.Status != review.StatusDecided || got.Subject.Name != "Ahmed" || !got.Subject.Established {
		t.Fatalf("ByMessage = %+v, %v; want the decided review with its subject", got, err)
	}
	if strings.Contains(got.Evidence.Text, "123 4567") {
		t.Errorf("stored evidence text %q keeps the number, want it redacted", got.Evidence.Text)
	}
	if got.Evidence.Thumbnail == nil {
		t.Error("thumbnail not stored")
	}
}

func TestStoreQueries(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	st := pgstore.Reviews{DB: pool}
	n := item()
	a, err := st.Create(ctx, review.Item{NewItem: n, Code: "K7Q2", Status: review.StatusPending, Deadline: start.Add(time.Hour), OnTimeout: review.TimeoutKeepDeletedNoBan, CreatedAt: start})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Create(ctx, review.Item{NewItem: n, Code: "K7Q2", Deadline: start, OnTimeout: review.TimeoutKeepDeletedNoBan, CreatedAt: start}); !errors.Is(err, review.ErrCodeTaken) {
		t.Fatalf("duplicate code: %v, want ErrCodeTaken", err)
	}
	if _, err := st.Create(ctx, review.Item{NewItem: n, Code: "ZZZ2", Status: review.StatusPending, Deadline: start.Add(3 * time.Hour), OnTimeout: review.TimeoutKeepDeletedNoBan, CreatedAt: start.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if got, err := st.ByCode(ctx, "K7Q2"); err != nil || got.ID != a.ID {
		t.Fatalf("ByCode = %v, %v", got.ID, err)
	}
	if _, err := st.ByCode(ctx, "NOPE"); !errors.Is(err, review.ErrNotFound) {
		t.Errorf("unknown code: %v, want ErrNotFound", err)
	}
	if due, _ := st.Due(ctx, start.Add(2*time.Hour)); len(due) != 1 || due[0].ID != a.ID {
		t.Errorf("Due = %d items, want only the one past its deadline", len(due))
	}
	if p, _ := st.Pending(ctx, group); len(p) != 2 || p[0].ID != a.ID {
		t.Errorf("Pending = %d items, want 2 oldest first", len(p))
	}
	got, won, err := st.Decide(ctx, a.ID, review.Decided{Status: review.StatusDecided, Decision: review.DecisionNotSpam, By: "1@lid", Method: review.MethodReply, At: start})
	if err != nil || !won || got.Evidence.Text != "" || got.Evidence.Thumbnail != nil {
		t.Fatalf("not-spam decision = text %q thumb %v won %v err %v; want the text and thumbnail cleared (DESIGN §7.5)", got.Evidence.Text, got.Evidence.Thumbnail != nil, won, err)
	}
	if _, won, err := st.Decide(ctx, a.ID, review.Decided{Status: review.StatusDecided, Decision: review.DecisionBan, At: start}); err != nil || won {
		t.Errorf("second decision: won=%v err=%v, want lost", won, err)
	}
	if err := st.AddPosted(ctx, review.Posted{ReviewID: 999, Chat: rgroup, ID: "X", SentAt: start}); !errors.Is(err, review.ErrNotFound) {
		t.Errorf("posted for an unknown review: %v, want ErrNotFound", err)
	}
	if err := st.AddPosted(ctx, review.Posted{ReviewID: a.ID, Chat: rgroup, ID: "M1", SentAt: start}); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkOutcomeEdited(ctx, rgroup, "M1"); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.PostedFor(ctx, a.ID); len(p) != 1 || !p[0].OutcomeEdited {
		t.Errorf("PostedFor = %+v, want one edited message", p)
	}
}

func TestEvidenceThreadsAudit(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	ev := pgstore.Evidence{DB: pool}
	l := review.NewLearning(review.LearningDeps{Evidence: ev, Corpus: &reviewtest.Corpus{}, Clock: clock.NewFake(start)})
	msg := wa.MessageRef{Sender: ahmed, ID: "M1"}
	if err := l.Snapshot(ctx, review.Snapshot{Group: group, Message: msg, Text: "mail a@b.com", Thumbnail: &wa.Image{Data: []byte{7}}}); err != nil {
		t.Fatal(err)
	}
	s, err := ev.Get(ctx, group, "M1")
	if err != nil || strings.Contains(s.Text, "a@b.com") || s.Thumbnail == nil || !s.ExpiresAt.Equal(start.Add(review.EvidenceRetention)) {
		t.Fatalf("snapshot = %+v, %v; want redacted text, thumbnail and 30-day expiry", s, err)
	}
	if err := ev.DeleteText(ctx, group, "M1"); err != nil {
		t.Fatal(err)
	}
	if s, _ := ev.Get(ctx, group, "M1"); s.Text != "" || s.Thumbnail != nil {
		t.Error("DeleteText left text or thumbnail")
	}
	if err := ev.DeleteText(ctx, group, "nope"); !errors.Is(err, review.ErrNotFound) {
		t.Errorf("DeleteText of a missing snapshot: %v, want ErrNotFound", err)
	}
	if n, _ := ev.Purge(ctx, start.Add(review.EvidenceRetention)); n != 1 {
		t.Errorf("purged %d, want 1", n)
	}

	th := pgstore.Threads{DB: pool}
	if _, ok, err := th.LastInbound(ctx, ahmed); ok || err != nil {
		t.Fatalf("unknown person: open=%v err=%v", ok, err)
	}
	if err := th.MarkException(ctx, ahmed, start); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := th.LastInbound(ctx, ahmed); ok {
		t.Error("an exception reply opened the thread, want only inbound DMs to open it (DESIGN §13.6)")
	}
	if err := th.MarkInbound(ctx, ahmed, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	at, ok, _ := th.LastInbound(ctx, ahmed)
	ex, exOK, _ := th.LastException(ctx, ahmed)
	if !ok || !at.Equal(start.Add(time.Minute)) || !exOK || !ex.Equal(start) {
		t.Errorf("thread = inbound %v %v, exception %v %v", at, ok, ex, exOK)
	}

	au := pgstore.Audit{DB: pool}
	if err := au.Record(ctx, review.AuditEntry{Group: group, Action: "blacklist.add", Diff: map[string]any{"n": 1}}); err != nil {
		t.Fatal(err)
	}
	got, err := store.ListAudit(ctx, pool, string(group), 0, 10)
	if err != nil || len(got) != 1 || got[0].Actor != store.ActorSystem || got[0].Action != "blacklist.add" {
		t.Errorf("audit = %+v, %v; want one system entry", got, err)
	}
}
