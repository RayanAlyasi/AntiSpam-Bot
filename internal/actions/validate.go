package actions

import (
	"fmt"
	"slices"
	"time"
)

// prepare applies Enqueue's defaults to a at now and validates it. dropped is non-empty when
// the action can no longer matter and must not be stored.
func prepare(a Action, now time.Time) (out Action, dropped string, err error) {
	if err := validate(a); err != nil {
		return Action{}, "", err
	}
	a.ID = 0
	a.Status = StatusPending
	a.Attempts = 0
	a.CreatedAt = now
	a.UpdatedAt = now
	a.LastError = ""
	a.Result = Result{}
	a.Payload.JIDs = slices.Clone(a.Payload.JIDs)
	a.Payload.Variants = slices.Clone(a.Payload.Variants)
	if a.NotBefore.IsZero() || a.NotBefore.Before(now) {
		a.NotBefore = now
	}
	if a.Deadline.IsZero() {
		a.Deadline = DefaultDeadline(a, now)
	}
	if !a.Deadline.After(now) {
		return Action{}, "deadline passed before enqueue", nil
	}
	return a, "", nil
}

func validate(a Action) error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}
	switch {
	case a.Bot == "":
		return invalid("no bot")
	case a.Chat == "":
		return invalid("no chat")
	case !a.Kind.Valid():
		return invalid("unknown kind %q", a.Kind)
	case !a.Priority.Valid():
		return invalid("priority %d is not P0–P4", a.Priority)
	case a.DedupeKey == "":
		return invalid("%s action without a dedupe key", a.Kind)
	}
	p := a.Payload
	switch a.Kind {
	case KindRevoke:
		if p.Target.ID == "" || p.Target.Sender == "" {
			return invalid("revoke needs the target message ID and sender")
		}
	case KindReact:
		if p.Target.ID == "" {
			return invalid("react needs the target message ID")
		}
	case KindEdit:
		if p.Target.ID == "" || p.Text == "" {
			return invalid("edit needs the bot's message ID and the new text")
		}
	case KindSend:
		if p.Text == "" && p.Image == nil {
			return invalid("send needs text or an image")
		}
	case KindKick, KindRejectJoin:
		if len(p.JIDs) == 0 {
			return invalid("%s needs at least one JID", a.Kind)
		}
		if !a.Chat.IsGroup() {
			return invalid("%s needs a group chat, not %s", a.Kind, a.Chat)
		}
	case KindAnnounce, KindJoinApproval, KindResetInvite:
		if !a.Chat.IsGroup() {
			return invalid("%s needs a group chat, not %s", a.Kind, a.Chat)
		}
	}
	if !a.Deadline.IsZero() && !a.NotBefore.IsZero() && a.Deadline.Before(a.NotBefore) {
		return invalid("deadline %s is before not_before %s", a.Deadline, a.NotBefore)
	}
	return nil
}
