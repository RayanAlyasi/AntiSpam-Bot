package queue_test

import (
	"context"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestOnFinishedRecordsReviewPosts: the queue's completion hook maps a sent review message to
// its review from the action alone, and ignores everything else.
func TestOnFinishedRecordsReviewPosts(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	if err := r.q.Enqueue(ctx, review.Send{Chat: rgroup, Message: wa.OutgoingMessage{Text: "review"}, ReviewID: 42, Purpose: "review"}); err != nil {
		t.Fatal(err)
	}
	if err := r.q.Enqueue(ctx, review.Send{Chat: admin, Message: wa.OutgoingMessage{Text: "a dm"}, Purpose: "reply"}); err != nil {
		t.Fatal(err)
	}
	all := r.store.All()
	post, dm := all[0], all[1]
	post.Status, post.Result.MessageID = actions.StatusDone, "BOT42"
	dm.Status, dm.Result.MessageID = actions.StatusDone, "DM1"
	failed := post
	failed.Status, failed.Result.MessageID = actions.StatusFailed, ""
	for _, a := range []actions.Action{failed, dm, post, post} {
		if err := r.q.OnFinished(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.posts) != 2 || r.posts[0].ReviewID != 42 || r.posts[0].ID != "BOT42" || r.posts[0].Chat != rgroup {
		t.Fatalf("posts = %+v, want review 42 mapped to BOT42 (twice is fine: RecordPosted is idempotent)", r.posts)
	}
}
