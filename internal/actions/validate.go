package actions

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// prepare applies Enqueue's defaults to a at now and validates it. dropped is non-empty when
// the action can no longer matter and must not be stored.
func prepare(a Action, now time.Time) (out Action, dropped string, err error) {
	// NUL can't be stored in Postgres text or jsonb; dropping it keeps both stores alike and
	// keeps attacker-supplied text from aborting the caller's transaction.
	a.Payload.Text = noNUL(a.Payload.Text)
	a.Payload.Emoji = noNUL(a.Payload.Emoji)
	variants := a.Payload.Variants
	a.Payload.Variants = nil
	for _, v := range variants {
		a.Payload.Variants = append(a.Payload.Variants, noNUL(v))
	}
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
	// Keys, JIDs and message IDs go to Postgres text columns as they are: no NUL, valid UTF-8.
	ids := []string{a.DedupeKey, string(a.Bot), string(a.Chat), string(a.Payload.Target.ID), string(a.Payload.Target.Sender)}
	if r := a.Payload.ReplyTo; r != nil {
		ids = append(ids, string(r.ID), string(r.Sender))
	}
	for _, j := range a.Payload.JIDs {
		ids = append(ids, string(j))
	}
	for _, id := range ids {
		if strings.ContainsRune(id, 0) || !utf8.ValidString(id) {
			return invalid("NUL or invalid UTF-8 in a key, JID or message ID")
		}
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

// noNUL drops NUL characters and replaces invalid UTF-8 the way JSON encoding would, so a text
// reads back the same from every store.
func noNUL(s string) string { return strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "\uFFFD") }
