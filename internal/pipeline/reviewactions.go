package pipeline

import (
	"context"
	"errors"
	"fmt"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// ErrUnsupportedReviewAction is returned for review actions the fast lane's wiring doesn't
// carry out yet: a ban or unban decided in a review needs the ban service (F-077, F-120).
var ErrUnsupportedReviewAction = errors.New("pipeline: review action not wired")

// ReviewActions puts the review service's WhatsApp writes on the paced action queue (DESIGN.md
// §9.5, §10): it implements review.Actions.
type ReviewActions struct {
	Bot   wa.JID
	Queue func(ctx context.Context, a actions.Action) error // enqueues on the action queue
}

var _ review.Actions = ReviewActions{}

// Enqueue implements review.Actions.
func (r ReviewActions) Enqueue(ctx context.Context, ra review.Action) error {
	a := actions.Action{Bot: r.Bot}
	switch x := ra.(type) {
	case review.Send:
		a.Chat, a.Kind, a.Priority = x.Chat, actions.KindSend, actions.P3
		a.Payload = actions.Payload{Text: x.Message.Text, ReplyTo: x.Message.ReplyTo, Image: x.Message.Image, Purpose: actions.PurposeReview}
		if x.Purpose != "" && x.Purpose != "review" && x.Purpose != "outcome" {
			a.Payload.Purpose = actions.PurposeNotice
		}
		a.DedupeKey = fmt.Sprintf("review-send:%d:%s:%s:%x", x.ReviewID, x.Chat, x.Purpose, hashText(x.Message.Text))
	case review.React:
		a.Chat, a.Kind, a.Priority = x.Chat, actions.KindReact, actions.P3
		a.Payload = actions.Payload{Target: x.Target, Emoji: x.Emoji}
		a.DedupeKey = fmt.Sprintf("review-react:%s:%s:%s", x.Chat, x.Target.ID, x.Emoji)
	case review.Edit:
		a.Chat, a.Kind, a.Priority = x.Chat, actions.KindEdit, actions.P3
		a.Payload = actions.Payload{Target: wa.MessageRef{ID: x.ID}, Text: x.Text}
		a.DedupeKey = fmt.Sprintf("review-edit:%s:%s:%x", x.Chat, x.ID, hashText(x.Text))
	case review.Delete:
		a.Chat, a.Kind, a.Priority = x.Group, actions.KindRevoke, actions.Priority(x.Priority)
		if !a.Priority.Valid() || a.Priority == actions.P0 {
			a.Priority = actions.P1
		}
		a.Payload = actions.Payload{Target: x.Message}
		a.DedupeKey = actions.RevokeKey(x.Group, x.Message.ID)
	case review.Kick:
		a.Chat, a.Kind, a.Priority = x.Group, actions.KindKick, actions.P2
		a.Payload = actions.Payload{JIDs: []wa.JID{x.Target}}
		a.DedupeKey = actions.KickKey(x.Group, x.Target, "review-"+x.Via)
	case review.RejectJoin:
		if len(x.Targets) == 0 {
			return nil
		}
		a.Chat, a.Kind, a.Priority = x.Group, actions.KindRejectJoin, actions.P2
		a.Payload = actions.Payload{JIDs: x.Targets}
		a.DedupeKey = actions.RejectJoinKey(x.Group, x.Targets[0], "review-"+x.Reason)
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedReviewAction, ra)
	}
	return r.Queue(ctx, a)
}

func hashText(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}
