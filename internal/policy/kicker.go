package policy

import (
	"context"
	"errors"
	"fmt"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Enqueuer is the part of the action queue a kick needs; actions.Store implements it.
type Enqueuer interface {
	Enqueue(ctx context.Context, a actions.Action) (actions.EnqueueResult, error)
	Get(ctx context.Context, id int64) (actions.Action, error)
}


// ErrKickDropped is returned when the queue refused a kick that could no longer matter.
var ErrKickDropped = errors.New("kick dropped by the action queue")

// QueueKicker carries out Bans' kicks on the paced action queue (DESIGN.md §10): a remove
// action for the bot that serves the group, coalesced with other kicks there by the executor.
type QueueKicker struct {
	Queue Enqueuer
	// BotFor returns the bot number that is primary in group (groups.bot_id, §15).
	BotFor func(ctx context.Context, group wa.JID) (wa.JID, error)
	// Wake, when set, nudges the executor after an enqueue.
	Wake func()
}

var _ Kicker = QueueKicker{}

// Kick enqueues the removal of member from group at priority, deduplicated on dedupeKey.
func (k QueueKicker) Kick(ctx context.Context, group, member wa.JID, priority int, dedupeKey string) error {
	if group == "" || member == "" || dedupeKey == "" {
		return fmt.Errorf("%w: kick needs a group, a member and a dedupe key", ErrInvalidBan)
	}
	bot, err := k.BotFor(ctx, group)
	if err != nil {
		return fmt.Errorf("bot for %s: %w", group, err)
	}
	key := dedupeKey
	var lastDead int64
	for {
		res, err := k.Queue.Enqueue(ctx, actions.Action{
			Bot:       bot,
			Chat:      group,
			Kind:      actions.KindKick,
			Priority:  actions.Priority(priority),
			Payload:   actions.Payload{JIDs: []wa.JID{member}},
			DedupeKey: key,
		})
		if err != nil {
			return fmt.Errorf("enqueue kick in %s: %w", group, err)
		}
		if res.Dropped != "" {
			return fmt.Errorf("%w: %s", ErrKickDropped, res.Dropped)
		}
		if res.Inserted {
			if k.Wake != nil {
				k.Wake()
			}
			return nil
		}
		// The same kick was enqueued before. If it is pending, running or done, this decision
		// is already carried out. If it died (the bot wasn't admin, it ran out of attempts, it
		// expired while the queue was paused), the person is still in the group: kick again
		// under a key derived from the dead action.
		prev, err := k.Queue.Get(ctx, res.ID)
		if err != nil {
			return fmt.Errorf("look up kick %d in %s: %w", res.ID, group, err)
		}
		switch prev.Status {
		case actions.StatusFailed, actions.StatusExpired, actions.StatusCancelled, actions.StatusRefused:
			// Each retry key names a newer dead action, so the walk always moves forward and
			// ends at a key that is new or alive. A store that hands back the same or an older
			// action would loop: stop instead.
			if prev.ID <= lastDead {
				return fmt.Errorf("enqueue kick in %s: the queue returned action %d again for %s", group, prev.ID, key)
			}
			lastDead = prev.ID
			key = fmt.Sprintf("%s:retry:%d", dedupeKey, prev.ID)
		default:
			return nil
		}
	}
}
