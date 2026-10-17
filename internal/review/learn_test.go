package review_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestSnapshotRedactsAndExpires covers F-114: every deletion keeps a redacted snapshot with its
// thumbnail for 30 days.
func TestSnapshotRedactsAndExpires(t *testing.T) {
	ctx := context.Background()
	c := clock.NewFake(start)
	ev := review.NewMemoryEvidence()
	l := review.NewLearning(review.LearningDeps{Evidence: ev, Corpus: &reviewtest.Corpus{}, Clock: c})
	msg := wa.MessageRef{Sender: ahmed, ID: "M1"}
	thumb := &wa.Image{Data: []byte{9}, Mimetype: "image/jpeg"}
	if err := l.Snapshot(ctx, review.Snapshot{Group: group, Message: msg, Text: "join +966 55 123 4567 now", Thumbnail: thumb}); err != nil {
		t.Fatal(err)
	}
	s, err := ev.Get(ctx, group, "M1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.Text, "123 4567") || !strings.Contains(s.Text, "join") {
		t.Errorf("snapshot text %q: want it kept but with the number masked (DESIGN §14 evidence is redacted)", s.Text)
	}
	if s.Thumbnail == nil || !s.ExpiresAt.Equal(start.Add(30*24*time.Hour)) {
		t.Errorf("snapshot thumbnail=%v expires=%v, want the thumbnail kept for 30 days", s.Thumbnail != nil, s.ExpiresAt)
	}
	if n, _ := ev.Purge(ctx, start.Add(29*24*time.Hour)); n != 0 {
		t.Errorf("purged %d snapshots after 29 days, want 0", n)
	}
	if n, _ := ev.Purge(ctx, start.Add(30*24*time.Hour)); n != 1 {
		t.Errorf("purged %d snapshots after 30 days, want 1", n)
	}
	if _, err := ev.Get(ctx, group, "M1"); !errors.Is(err, review.ErrNotFound) {
		t.Errorf("Get after purge: %v, want ErrNotFound", err)
	}
}

// TestConfirmSpamCapturesCorpusEntry covers F-114's confirmed-spam capture.
func TestConfirmSpamCapturesCorpusEntry(t *testing.T) {
	ctx := context.Background()
	corpus := &reviewtest.Corpus{}
	l := review.NewLearning(review.LearningDeps{Evidence: review.NewMemoryEvidence(), Corpus: corpus, Clock: clock.NewFake(start)})
	if err := l.ConfirmSpam(ctx, review.Confirmation{Group: group, Message: wa.MessageRef{Sender: ahmed, ID: "M2"}, Text: "buy now", Admin: sara, Source: "command"}); err != nil {
		t.Fatal(err)
	}
	if a := corpus.Active(); len(a) != 1 || a[0].Admin != sara || a[0].Source != "command" {
		t.Errorf("corpus = %+v, want one active entry with its provenance (DESIGN §7.5)", a)
	}
}

// failingEvidence fails every call, to show an overturn still finishes the other steps.
type failingEvidence struct{ review.EvidenceStore }

func (failingEvidence) Get(context.Context, wa.JID, wa.MessageID) (review.Snapshot, error) {
	return review.Snapshot{}, errors.New("db down")
}

func (failingEvidence) DeleteText(context.Context, wa.JID, wa.MessageID) error {
	return errors.New("db down")
}

func TestOverturnRunsEveryStepDespiteErrors(t *testing.T) {
	ctx := context.Background()
	corpus, standing := &reviewtest.Corpus{}, &reviewtest.Standing{}
	l := review.NewLearning(review.LearningDeps{Evidence: failingEvidence{}, Corpus: corpus, Standing: standing})
	msg := wa.MessageRef{Sender: ahmed, ID: "M3"}
	if _, err := corpus.Confirm(ctx, review.Confirmation{Group: group, Message: msg, Text: "x"}); err != nil {
		t.Fatal(err)
	}
	err := l.Overturn(ctx, review.Overturn{Group: group, Message: msg, Actor: sara})
	if err == nil {
		t.Error("Overturn with a failing evidence store: want the error reported")
	}
	if len(corpus.Active()) != 0 || len(standing.Events()) != 3 {
		t.Errorf("active=%d standing=%d, want the corpus entry retracted and trust restored anyway", len(corpus.Active()), len(standing.Events()))
	}
}
