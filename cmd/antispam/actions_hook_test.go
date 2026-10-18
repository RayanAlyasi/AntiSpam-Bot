package main

import (
	"context"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
)

// TestActionFinishedReachesTheReviewQueue: once the fast lane registered the review queue's
// hook, finished actions reach it; before that, they are dropped without error.
func TestActionFinishedReachesTheReviewQueue(t *testing.T) {
	s := &waStack{}
	ctx := context.Background()
	if err := s.actionFinished(ctx, actions.Action{ID: 1}); err != nil {
		t.Fatalf("without a hook: %v, want nil", err)
	}
	var got []int64
	hook := func(_ context.Context, a actions.Action) error { got = append(got, a.ID); return nil }
	s.finished.Store(&hook)
	if err := s.actionFinished(ctx, actions.Action{ID: 2, Status: actions.StatusDone}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 2 {
		t.Errorf("hook saw %v, want [2] (review posts must be linked as soon as they're sent)", got)
	}
}
