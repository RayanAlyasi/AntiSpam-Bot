package reviewjobs

import (
	"context"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

func TestRegisterAndWork(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	c := clock.NewFake(start)
	store, ev := review.NewMemoryStore(), review.NewMemoryEvidence()
	acts := &reviewtest.Actions{}
	svc := review.NewService(review.Deps{Store: store, Settings: &reviewtest.Settings{}, Admins: &reviewtest.Admins{}, Actions: acts, Clock: c})

	var periodic []*river.PeriodicJob
	if err := Register(svc, ev, c)(river.NewWorkers(), &periodic); err != nil {
		t.Fatal(err)
	}
	if len(periodic) != 2 {
		t.Fatalf("registered %d periodic jobs, want expire and purge", len(periodic))
	}

	if _, err := svc.Open(ctx, review.NewItem{Group: "1@g.us", Kind: review.KindBanDecision, Subject: review.Subject{Person: "2@lid"}}); err != nil {
		t.Fatal(err)
	}
	if err := ev.Save(ctx, review.Snapshot{Group: "1@g.us", Message: wa.MessageRef{ID: "M"}, At: start, ExpiresAt: start.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	c.Advance(7 * time.Hour)
	if err := (&expireWorker{svc: svc}).Work(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if p, _ := svc.Pending(ctx, ""); len(p) != 0 {
		t.Errorf("after the expire job %d reviews still pending, want the timeout applied", len(p))
	}
	if err := (&purgeWorker{evidence: ev, clock: c}).Work(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ev.Get(ctx, "1@g.us", "M"); err == nil {
		t.Error("after the purge job the expired snapshot remains, want it deleted (30-day retention)")
	}
}
