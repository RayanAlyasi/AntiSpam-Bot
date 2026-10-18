package chat

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// subject is who a moderation command acts on, and the message it replied to, if any.
type subject struct {
	who  wa.JID
	msg  *wa.MessageRef
	text string
}

// target resolves a command's subject: the quoted message's sender (or, in a review group, the
// person a quoted review is about), the first @mention, or a phone number argument.
func (h *Handler) target(ctx context.Context, c *call) (subject, error) {
	if q := c.m.Quoted; q != nil {
		if c.where == inReviewGroup && h.d.Reviews != nil {
			if it, err := h.reviewFor(ctx, c.m.Chat, q.ID); err == nil {
				s := subject{who: it.Subject.Person, msg: it.Message, text: it.Evidence.Text}
				if s.who == "" && it.Message != nil {
					s.who = it.Message.Sender
				}
				return s, nil
			}
		}
		if q.Sender != "" {
			return subject{who: q.Sender, msg: &wa.MessageRef{Sender: q.Sender, ID: q.ID}, text: q.Text}, nil
		}
	}
	if len(c.m.Mentions) > 0 {
		return subject{who: c.m.Mentions[0]}, nil
	}
	for _, a := range numberArgs(c.cmd.Args) {
		e164, err := h.d.Normalize.Normalize(a, h.defaultCountry(ctx, c.group))
		if err != nil {
			continue
		}
		if h.d.Identity != nil {
			if lid, ok, err := h.d.Identity.ByPhone(ctx, e164); err != nil {
				return subject{}, fmt.Errorf("identity by phone: %w", err)
			} else if ok {
				return subject{who: lid}, nil
			}
		}
		return subject{who: wa.JID(strings.TrimPrefix(e164, "+") + "@" + wa.ServerUser)}, nil
	}
	return subject{}, nil
}

func (h *Handler) defaultCountry(ctx context.Context, group wa.JID) string {
	if h.d.Settings == nil || group == "" {
		return ""
	}
	v, _ := h.d.Settings.Get(ctx, group, "blacklist.default_country")
	return v
}

// looksLikeNumber reports whether an argument is probably a phone number or wa.me link.
func looksLikeNumber(s string) bool {
	if strings.Contains(strings.ToLower(s), "wa.me/") {
		return true
	}
	n := 0
	for _, r := range s {
		if (r >= '0' && r <= '9') || (r >= '٠' && r <= '٩') || (r >= '۰' && r <= '۹') {
			n++
		}
	}
	return n >= 7
}

// protectedPerson reports whether the bot must never act on who: itself or a group admin (§8.6).
func (h *Handler) protectedPerson(ctx context.Context, group, who wa.JID) (bool, error) {
	if who == "" || (h.d.Self != "" && who == h.d.Self) {
		return true, nil
	}
	return h.isAdmin(ctx, group, who)
}

// reasonOf finds a ban reason among the arguments.
func reasonOf(args []string) (review.BanReason, bool) {
	for _, a := range args {
		switch strings.ToLower(a) {
		case "spam", "سبام":
			return review.ReasonSpam, true
		case "other", "أخرى", "اخرى", "آخر", "اخر":
			return review.ReasonOther, true
		}
	}
	return review.ReasonOther, false
}

// confirmSpam deletes the replied message, keeps its evidence and records it as confirmed spam.
func (h *Handler) confirmSpam(ctx context.Context, c *call, s subject) error {
	if s.msg == nil {
		return nil
	}
	errs := []error{h.d.Actions.Enqueue(ctx, review.Delete{Group: c.group, Message: *s.msg, Priority: review.P1, Reason: "command"})}
	if h.d.Evidence != nil {
		errs = append(errs, h.d.Evidence.Snapshot(ctx, review.Snapshot{Group: c.group, Message: *s.msg, Text: s.text, Reason: "command"}))
	}
	if h.d.Learning != nil {
		errs = append(errs, h.d.Learning.ConfirmSpam(ctx, review.Confirmation{
			Group: c.group, Message: *s.msg, Text: s.text, Admin: c.person, Source: "command", At: h.now(),
		}))
	}
	return errors.Join(errs...)
}

// moderate resolves the subject and refuses admins and the bot. ok is false when the command
// must stop (it already replied).
func (h *Handler) moderate(ctx context.Context, c *call, usage string) (subject, bool, error) {
	s, err := h.target(ctx, c)
	if err != nil {
		return s, false, err
	}
	if s.who == "" {
		return s, false, h.reply(ctx, c, usage, nil)
	}
	never, err := h.protectedPerson(ctx, c.group, s.who)
	if err != nil {
		return s, false, err
	}
	if never {
		return s, false, h.reply(ctx, c, "cmd.never_admin", nil)
	}
	return s, true, nil
}

// cmdBan is a local ban: kick plus rejoin block. The reason is required: without one the ban
// still happens at once with reason "other", and the admin is asked; answering "spam" within
// banReasonWindow upgrades it (§13.7).
func (h *Handler) cmdBan(ctx context.Context, c *call) error {
	s, ok, err := h.moderate(ctx, c, "cmd.usage.ban")
	if !ok {
		return err
	}
	reason, given := reasonOf(c.cmd.Args)
	var errs []error
	if reason == review.ReasonSpam {
		errs = append(errs, h.confirmSpam(ctx, c, s))
	}
	errs = append(errs,
		h.d.Actions.Enqueue(ctx, review.Ban{Group: c.group, Target: s.who, Reason: reason, Actor: c.person, Via: "command"}),
		h.audit(ctx, c, "command.ban", map[string]any{"target": string(s.who), "reason": string(reason), "reason_given": given}))
	if !given {
		h.mu.Lock()
		h.bans[c.person] = pendingBan{group: c.group, target: s.who, msg: s.msg, text: s.text, at: h.now()}
		h.mu.Unlock()
		errs = append(errs, h.reply(ctx, c, "cmd.ban_ask_reason", nil))
	} else {
		errs = append(errs, h.reply(ctx, c, "cmd.banned", map[string]string{"reason": string(reason)}))
	}
	return errors.Join(errs...)
}

// banReason consumes a "spam"/"other" DM answering a .ban that had no reason. It reports
// whether the text was such an answer.
func (h *Handler) banReason(ctx context.Context, person wa.JID, m Message, text string, errs *[]error) bool {
	reason, ok := reasonOf([]string{text})
	if !ok || strings.Contains(text, " ") {
		return false
	}
	h.mu.Lock()
	p, pending := h.bans[person]
	if pending {
		delete(h.bans, person)
	}
	h.mu.Unlock()
	if !pending || h.now().Sub(p.at) > banReasonWindow {
		return false
	}
	c := &call{m: m, person: person, group: p.group, where: inDM}
	c.lang = h.lang(ctx, c)
	if reason == review.ReasonSpam {
		*errs = append(*errs,
			h.confirmSpam(ctx, c, subject{who: p.target, msg: p.msg, text: p.text}),
			h.d.Actions.Enqueue(ctx, review.Ban{Group: p.group, Target: p.target, Reason: review.ReasonSpam, Actor: person, Via: "command"}),
			h.audit(ctx, c, "command.ban_reason", map[string]any{"target": string(p.target), "reason": "spam"}))
	}
	*errs = append(*errs, h.reply(ctx, c, "cmd.banned", map[string]string{"reason": string(reason)}))
	return true
}

// cmdKick removes someone without a rejoin block.
func (h *Handler) cmdKick(ctx context.Context, c *call) error {
	s, ok, err := h.moderate(ctx, c, "cmd.usage.kick")
	if !ok {
		return err
	}
	reason, _ := reasonOf(c.cmd.Args)
	var errs []error
	if reason == review.ReasonSpam {
		errs = append(errs, h.confirmSpam(ctx, c, s))
	}
	return errors.Join(append(errs,
		h.d.Actions.Enqueue(ctx, review.Kick{Group: c.group, Target: s.who, Reason: reason, Actor: c.person, Via: "command"}),
		h.audit(ctx, c, "command.kick", map[string]any{"target": string(s.who), "reason": string(reason)}),
		h.reply(ctx, c, "cmd.kicked", nil))...)
}

// cmdUnban lifts a local ban; it never re-adds the person.
func (h *Handler) cmdUnban(ctx context.Context, c *call) error {
	s, err := h.target(ctx, c)
	if err != nil {
		return err
	}
	if s.who == "" {
		return h.reply(ctx, c, "cmd.usage.unban", nil)
	}
	return errors.Join(
		h.d.Actions.Enqueue(ctx, review.Unban{Group: c.group, Target: s.who, Actor: c.person, Via: "command"}),
		h.audit(ctx, c, "command.unban", map[string]any{"target": string(s.who)}),
		h.reply(ctx, c, "cmd.unbanned", nil))
}

func (h *Handler) memberAllow(ctx context.Context, c *call, allow bool) error {
	if h.d.Members == nil {
		return h.unavailable(ctx, c)
	}
	s, err := h.target(ctx, c)
	if err != nil {
		return err
	}
	if s.who == "" {
		return h.reply(ctx, c, "cmd.usage.allow", nil)
	}
	if allow {
		err = h.d.Members.Allow(ctx, c.group, s.who, c.person)
		return errors.Join(err, h.audit(ctx, c, "command.allow", map[string]any{"target": string(s.who)}), h.reply(ctx, c, "cmd.allowed", nil))
	}
	err = h.d.Members.Unallow(ctx, c.group, s.who, c.person)
	return errors.Join(err, h.audit(ctx, c, "command.unallow", map[string]any{"target": string(s.who)}), h.reply(ctx, c, "cmd.unallowed", nil))
}

func (h *Handler) cmdUnallow(ctx context.Context, c *call) error { return h.memberAllow(ctx, c, false) }

// cmdUndo reverses the bot's last reversible action in the group: a ban or kick is lifted (no
// re-add), a strike gives the member's trust back.
func (h *Handler) cmdUndo(ctx context.Context, c *call) error {
	if h.d.History == nil {
		return h.unavailable(ctx, c)
	}
	r, ok, err := h.d.History.LastReversible(ctx, c.group)
	if err != nil {
		return fmt.Errorf("last reversible action: %w", err)
	}
	if !ok {
		return h.reply(ctx, c, "cmd.nothing_to_undo", nil)
	}
	var errs []error
	switch r.Kind {
	case "ban", "kick":
		errs = append(errs, h.d.Actions.Enqueue(ctx, review.Unban{Group: c.group, Target: r.Target, Actor: c.person, Via: "command"}))
	case "strike":
		if h.d.Standing == nil {
			return h.unavailable(ctx, c)
		}
		errs = append(errs, h.d.Standing.RestoreTrust(ctx, c.group, r.Target))
	default:
		return h.reply(ctx, c, "cmd.nothing_to_undo", nil)
	}
	errs = append(errs,
		h.d.History.MarkUndone(ctx, r.ID, c.person),
		h.audit(ctx, c, "command.undo", map[string]any{"kind": r.Kind, "target": string(r.Target), "action_id": r.ID}),
		h.reply(ctx, c, "cmd.undone", map[string]string{"kind": r.Kind}))
	return errors.Join(errs...)
}

// cmdSpam handles a reply .spam: delete, ban (allowed for established members because it is an
// admin decision) and record confirmed spam (§13.7, §7.5).
func (h *Handler) cmdSpam(ctx context.Context, c *call) error {
	if c.m.Quoted == nil {
		return h.reply(ctx, c, "cmd.usage.spam", nil)
	}
	s, ok, err := h.moderate(ctx, c, "cmd.usage.spam")
	if !ok {
		return err
	}
	return errors.Join(
		h.confirmSpam(ctx, c, s),
		h.d.Actions.Enqueue(ctx, review.Ban{Group: c.group, Target: s.who, Reason: review.ReasonSpam, Actor: c.person, Via: "command"}),
		h.audit(ctx, c, "command.spam", map[string]any{"target": string(s.who)}),
		h.reply(ctx, c, "cmd.spam_done", nil))
}

// cmdNotSpam handles a reply .notspam / .ok: an overturn (§7.5).
func (h *Handler) cmdNotSpam(ctx context.Context, c *call) error {
	if c.m.Quoted == nil {
		return h.reply(ctx, c, "cmd.usage.notspam", nil)
	}
	s, err := h.target(ctx, c)
	if err != nil {
		return err
	}
	if s.msg == nil || h.d.Learning == nil {
		return h.reply(ctx, c, "cmd.usage.notspam", nil)
	}
	return errors.Join(
		h.d.Learning.Overturn(ctx, review.Overturn{Group: c.group, Message: *s.msg, Actor: c.person, Method: review.MethodCommand}),
		h.audit(ctx, c, "command.notspam", map[string]any{"target": string(s.who), "message": string(s.msg.ID)}),
		h.reply(ctx, c, "cmd.notspam_done", nil))
}

// domainOf reduces a typed domain or URL to its host.
func domainOf(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	host := strings.TrimPrefix(u.Hostname(), "www.")
	if !strings.Contains(host, ".") {
		return ""
	}
	return host
}

func (h *Handler) cmdDomain(ctx context.Context, c *call) error {
	if h.d.Lists == nil {
		return h.unavailable(ctx, c)
	}
	if len(c.cmd.Args) != 2 {
		return h.reply(ctx, c, "cmd.usage.domain", nil)
	}
	d := domainOf(c.cmd.Args[1])
	if d == "" {
		return h.reply(ctx, c, "cmd.usage.domain", nil)
	}
	var err error
	switch strings.ToLower(c.cmd.Args[0]) {
	case "allow":
		err = h.d.Lists.AllowDomain(ctx, c.group, d, c.person)
	case "block":
		err = h.d.Lists.BlockDomain(ctx, c.group, d, c.person)
	default:
		return h.reply(ctx, c, "cmd.usage.domain", nil)
	}
	if err != nil {
		return fmt.Errorf("domain %s: %w", d, err)
	}
	return errors.Join(
		h.audit(ctx, c, "lists.domain_"+strings.ToLower(c.cmd.Args[0]), map[string]any{"domain": d}),
		h.reply(ctx, c, "cmd.list_saved", map[string]string{"value": d}))
}

// cmdWord edits the group's blocked words. .word block first replies with a dry-run count over
// the recent messages and saves only when the same command is repeated (§8.4).
func (h *Handler) cmdWord(ctx context.Context, c *call) error {
	if h.d.Lists == nil {
		return h.unavailable(ctx, c)
	}
	if len(c.cmd.Args) < 2 {
		return h.reply(ctx, c, "cmd.usage.word", nil)
	}
	op := strings.ToLower(c.cmd.Args[0])
	phrase := strings.TrimSpace(strings.TrimPrefix(c.cmd.Rest, c.cmd.Args[0]))
	switch op {
	case "unblock":
		if err := h.d.Lists.UnblockWord(ctx, c.group, phrase, c.person); err != nil {
			return fmt.Errorf("unblock word: %w", err)
		}
		return errors.Join(
			h.audit(ctx, c, "lists.word_unblock", map[string]any{"phrase": phrase}),
			h.reply(ctx, c, "cmd.list_saved", map[string]string{"value": phrase}))
	case "block":
	default:
		return h.reply(ctx, c, "cmd.usage.word", nil)
	}
	h.mu.Lock()
	p, ok := h.words[c.person]
	confirmed := ok && p.group == c.group && strings.EqualFold(p.phrase, phrase) && h.now().Sub(p.at) <= wordConfirmWindow
	if confirmed {
		delete(h.words, c.person)
	}
	h.mu.Unlock()
	if confirmed {
		if err := h.d.Lists.BlockWord(ctx, c.group, phrase, c.person); err != nil {
			return fmt.Errorf("block word: %w", err)
		}
		return errors.Join(
			h.audit(ctx, c, "lists.word_block", map[string]any{"phrase": phrase}),
			h.reply(ctx, c, "cmd.list_saved", map[string]string{"value": phrase}))
	}
	n := 0
	if h.d.Recent != nil {
		var err error
		if n, err = h.d.Recent.CountMatching(ctx, c.group, phrase); err != nil {
			return fmt.Errorf("dry run for %q: %w", phrase, err)
		}
	}
	h.mu.Lock()
	h.words[c.person] = pendingWord{group: c.group, phrase: phrase, at: h.now()}
	h.mu.Unlock()
	return h.reply(ctx, c, "cmd.word_dry_run", map[string]string{"n": fmt.Sprint(n), "phrase": phrase, "prefix": h.d.Prefix})
}

// numberArgs joins arguments that together spell one phone number ("+966 55 123 4567") and
// returns the candidates that look like numbers.
func numberArgs(args []string) []string {
	var out []string
	cur := ""
	flush := func() {
		if looksLikeNumber(cur) {
			out = append(out, cur)
		}
		cur = ""
	}
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "+") || strings.Contains(strings.ToLower(a), "wa.me/"):
			flush()
			cur = a
		case isNumberFragment(a):
			cur += a
		default:
			flush()
		}
	}
	flush()
	return out
}
