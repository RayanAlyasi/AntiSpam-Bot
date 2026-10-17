package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// cmdBlacklist handles .blacklist add|remove|list|check (§9.8). --instance is operator-only.
func (h *Handler) cmdBlacklist(ctx context.Context, c *call) error {
	if h.d.Blacklist == nil {
		return h.unavailable(ctx, c)
	}
	if len(c.cmd.Args) == 0 {
		return h.reply(ctx, c, "cmd.usage.blacklist", nil)
	}
	instance := false
	var numbers, reason []string
	for _, a := range c.cmd.Args[1:] {
		switch {
		case a == "--instance":
			instance = true
		case looksLikeNumber(a) || (strings.HasPrefix(a, "+") && len(reason) == 0):
			numbers = append(numbers, a)
		case len(numbers) > 0 && len(reason) == 0 && isNumberFragment(a):
			// "+966 55 123 4567": the pieces of one number written with spaces.
			numbers[len(numbers)-1] += a
		default:
			reason = append(reason, a)
		}
	}
	if instance && !h.operator(c.person) {
		return h.reply(ctx, c, "cmd.blacklist_operator_only", nil)
	}
	switch strings.ToLower(c.cmd.Args[0]) {
	case "add":
		if len(numbers) == 0 {
			return h.reply(ctx, c, "cmd.usage.blacklist", nil)
		}
		res, err := h.d.Blacklist.Add(ctx, blacklist.AddRequest{
			Group: c.group, Numbers: numbers, Reason: strings.Join(reason, " "), By: c.person,
			Operator: h.operator(c.person), Instance: instance,
		})
		if errors.Is(err, blacklist.ErrNotOperator) {
			return h.reply(ctx, c, "cmd.blacklist_operator_only", nil)
		}
		added := make([]string, len(res.Added))
		for i, e := range res.Added {
			added[i] = h.showNumber(c, e.E164)
		}
		return errors.Join(err, h.reply(ctx, c, "cmd.blacklist_added", map[string]string{
			"added":   listOrDash(added),
			"already": listOrDash(h.showNumbers(c, res.Already)),
			"invalid": listOrDash(res.Invalid),
			"banned":  fmt.Sprint(len(res.Banned)),
			"skipped": fmt.Sprint(len(res.Skipped)),
			"review":  listOrDash(res.Review),
		}))
	case "remove":
		if len(numbers) == 0 {
			return h.reply(ctx, c, "cmd.usage.blacklist", nil)
		}
		res, err := h.d.Blacklist.Remove(ctx, blacklist.RemoveRequest{
			Group: c.group, Numbers: numbers, By: c.person, Operator: h.operator(c.person), Instance: instance,
		})
		if errors.Is(err, blacklist.ErrNotOperator) {
			return h.reply(ctx, c, "cmd.blacklist_operator_only", nil)
		}
		return errors.Join(err, h.reply(ctx, c, "cmd.blacklist_removed", map[string]string{
			"removed":  listOrDash(h.showNumbers(c, res.Removed)),
			"notfound": listOrDash(h.showNumbers(c, res.NotFound)),
			"invalid":  listOrDash(res.Invalid),
		}))
	case "list":
		es, err := h.d.Blacklist.ForGroup(ctx, c.group)
		if err != nil {
			return fmt.Errorf("blacklist list: %w", err)
		}
		if len(es) == 0 {
			return h.reply(ctx, c, "cmd.blacklist_empty", nil)
		}
		lines := make([]string, 0, len(es))
		for _, e := range es {
			line := h.showNumber(c, e.E164) + " · " + review.MaskNumber("+"+e.AddedBy.User())
			if e.Reason != "" {
				line += " · " + e.Reason
			}
			lines = append(lines, line)
		}
		return h.reply(ctx, c, "cmd.blacklist_list", map[string]string{"entries": strings.Join(lines, "\n")})
	case "check":
		if len(numbers) != 1 {
			return h.reply(ctx, c, "cmd.usage.blacklist", nil)
		}
		res, err := h.d.Blacklist.Check(ctx, c.group, numbers[0])
		if errors.Is(err, blacklist.ErrInvalid) {
			return h.reply(ctx, c, "cmd.blacklist_invalid", map[string]string{"input": numbers[0]})
		}
		if err != nil {
			return fmt.Errorf("blacklist check: %w", err)
		}
		key := "cmd.blacklist_check_no"
		switch {
		case res.Group != nil:
			key = "cmd.blacklist_check_group"
		case res.Instance != nil:
			key = "cmd.blacklist_check_instance"
		}
		member := h.t(c, "cmd.no", nil)
		if res.Member {
			member = h.t(c, "cmd.yes", nil)
		}
		return h.reply(ctx, c, key, map[string]string{"number": h.showNumber(c, res.E164), "member": member})
	}
	return h.reply(ctx, c, "cmd.usage.blacklist", nil)
}

func isNumberFragment(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < '٠' || r > '٩') && r != '-' && r != '(' && r != ')' {
			return false
		}
	}
	return s != ""
}

// showNumber masks a number for group admins and shows it in full to the operator (§9.8 Audit).
func (h *Handler) showNumber(c *call, e164 string) string {
	if h.operator(c.person) && c.where == inDM {
		return e164
	}
	return review.MaskNumber(e164)
}

func (h *Handler) showNumbers(c *call, ns []string) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = h.showNumber(c, n)
	}
	return out
}

func listOrDash(xs []string) string {
	if len(xs) == 0 {
		return "–"
	}
	return strings.Join(xs, ", ")
}

// start answers a START DM with the three setup steps (§13.6 step 1).
func (h *Handler) start(ctx context.Context, m Message) error {
	text := h.d.Texts.T(h.d.Language, "onboard.start", map[string]string{"url": h.d.WebsiteURL, "prefix": h.d.Prefix})
	if isArabic(m.Text) {
		text = h.d.Texts.T("ar", "onboard.start", map[string]string{"url": h.d.WebsiteURL, "prefix": h.d.Prefix})
	}
	return h.d.Actions.Enqueue(ctx, review.Send{Chat: m.Chat, Message: wa.OutgoingMessage{Text: text}, Purpose: "onboarding"})
}

func isArabic(s string) bool {
	for _, r := range s {
		if r >= 0x0600 && r <= 0x06FF {
			return true
		}
	}
	return false
}

// joinFromLink joins a group from an invite link an admin sent by DM, but only if the sender
// administers it, and within the per-bot join limit (§13.6 step 2).
func (h *Handler) joinFromLink(ctx context.Context, m Message, person wa.JID) error {
	c := &call{m: m, person: person, where: inDM, lang: h.d.Language}
	if isArabic(m.Text) {
		c.lang = "ar"
	}
	if h.d.Joiner == nil {
		return h.unavailable(ctx, c)
	}
	code := inviteLink.FindStringSubmatch(m.Text)[1]
	now := h.now()
	h.mu.Lock()
	recent := h.joins[:0]
	for _, t := range h.joins {
		if now.Sub(t) < reminderInterval {
			recent = append(recent, t)
		}
	}
	h.joins = recent
	full := len(h.joins) >= maxJoinsPerDay
	if !full {
		h.joins = append(h.joins, now)
	}
	h.mu.Unlock()
	if full {
		return h.reply(ctx, c, "onboard.join_limit", nil)
	}
	info, err := h.d.Joiner.GroupFromLink(ctx, code)
	if err != nil {
		return errors.Join(fmt.Errorf("resolve invite: %w", err), h.reply(ctx, c, "onboard.join_bad_link", nil))
	}
	ids := map[wa.JID]bool{}
	for _, j := range []wa.JID{person, m.Sender, m.SenderPhone} {
		if j != "" {
			ids[j] = true
		}
	}
	if len(info.Participants) > 0 {
		admin := false
		for _, p := range info.Participants {
			if (ids[p.JID] || ids[p.LID] || ids[p.PhoneNumber]) && (p.IsAdmin || p.IsSuperAdmin) {
				admin = true
			}
		}
		if !admin {
			return h.reply(ctx, c, "onboard.join_not_admin", map[string]string{"group": info.Name})
		}
	}
	group, err := h.d.Joiner.JoinWithLink(ctx, code)
	if err != nil {
		return fmt.Errorf("join with link: %w", err)
	}
	if len(info.Participants) == 0 {
		// The preview didn't list participants: check after joining and leave if it fails.
		admin, err := h.senderIsAdmin(ctx, group, m, person)
		if err != nil || !admin {
			return errors.Join(err, h.d.Joiner.Leave(ctx, group), h.reply(ctx, c, "onboard.join_not_admin", map[string]string{"group": info.Name}))
		}
	}
	return h.reply(ctx, c, "onboard.joined", map[string]string{"group": info.Name})
}

// OnPromoted handles the bot being made admin of a group (§13.6 step 3): the promoting admin is
// told by DM only if they have an open thread; otherwise the message waits until they write.
func (h *Handler) OnPromoted(ctx context.Context, group, by wa.JID) error {
	if by == "" {
		return nil
	}
	return h.Notify(ctx, by, group, "onboard.promoted", map[string]string{"prefix": h.d.Prefix})
}

// OnNotAdmin handles the bot being in a group without admin rights (§13.6 step 4): at most one
// reminder per day, and only in an existing DM thread with the person who added it.
func (h *Handler) OnNotAdmin(ctx context.Context, group, addedBy wa.JID) error {
	if addedBy == "" {
		return nil
	}
	person := h.canonical(ctx, addedBy)
	open, err := h.gate.Open(ctx, person)
	if err != nil || !open {
		return err
	}
	key := string(person) + "|" + string(group)
	now := h.now()
	h.mu.Lock()
	last, seen := h.reminders[key]
	due := !seen || now.Sub(last) >= reminderInterval
	if due {
		h.reminders[key] = now
	}
	h.mu.Unlock()
	if !due {
		return nil
	}
	return h.Notify(ctx, addedBy, group, "onboard.not_admin", nil)
}
