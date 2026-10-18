package chat

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
)

// fallbacks maps the words .review timeout accepts to review.on_timeout values.
var fallbacks = map[string]review.OnTimeout{
	"keep": review.TimeoutKeepDeletedNoBan, "delete": review.TimeoutDeleteNoBan,
	"ban": review.TimeoutAutoBan, "dismiss": review.TimeoutDismiss,
	string(review.TimeoutKeepDeletedNoBan): review.TimeoutKeepDeletedNoBan,
	string(review.TimeoutDeleteNoBan):      review.TimeoutDeleteNoBan,
	string(review.TimeoutAutoBan):          review.TimeoutAutoBan,
}

// parseDuration accepts Go durations plus days ("2d").
func parseDuration(s string) (time.Duration, bool) {
	if n, ok := strings.CutSuffix(strings.ToLower(s), "d"); ok {
		days, err := strconv.Atoi(n)
		return time.Duration(days) * 24 * time.Hour, err == nil && days > 0 && days <= 7
	}
	d, err := time.ParseDuration(strings.ToLower(s))
	return d, err == nil && d >= time.Minute && d <= 7*24*time.Hour
}

// cmdReview handles .review group|dashboard|dm, .review timeout <d> <fallback> and
// .review <CODE> <decision>.
func (h *Handler) cmdReview(ctx context.Context, c *call) error {
	if len(c.cmd.Args) == 0 {
		return h.reply(ctx, c, "cmd.usage.review", nil)
	}
	switch strings.ToLower(c.cmd.Args[0]) {
	case "group", "dashboard", "dm":
		if c.group == "" {
			return h.pickGroup(ctx, c)
		}
		return h.reviewDestination(ctx, c, strings.ToLower(c.cmd.Args[0]))
	case "timeout":
		if c.group == "" {
			return h.pickGroup(ctx, c)
		}
		return h.reviewTimeout(ctx, c)
	}
	return h.reviewDecide(ctx, c)
}

func (h *Handler) reviewDestination(ctx context.Context, c *call, dest string) error {
	if h.d.Settings == nil {
		return h.unavailable(ctx, c)
	}
	switch dest {
	case "dm":
		// Reviewer DMs with opt-in arrive with F-183 (v0.3).
		return h.reply(ctx, c, "cmd.review_dm_later", map[string]string{"prefix": h.d.Prefix})
	case "group":
		rg, err := h.d.Settings.Get(ctx, c.group, "review.review_group")
		if err != nil && !errors.Is(err, ErrUnknownKey) {
			return fmt.Errorf("get review group: %w", err)
		}
		if rg == "" {
			// Until a review group is set, reviews stay on the dashboard; say how to set one.
			return errors.Join(h.setAndReply(ctx, c, "review.destination", dest), h.reply(ctx, c, "cmd.review_group_missing", nil))
		}
	case "dashboard":
		if h.d.WebsiteURL == "" {
			// No website yet (v0.1): the setting still stops posting to a review group, and the
			// reply says where the cases wait instead.
			if ok, err := h.set(ctx, c, "review.destination", dest); err != nil || !ok {
				return err
			}
			return h.reply(ctx, c, "cmd.review_dashboard_no_site", map[string]string{"group": h.groupName(ctx, c.group)})
		}
	}
	return h.setAndReply(ctx, c, "review.destination", dest)
}

func (h *Handler) reviewTimeout(ctx context.Context, c *call) error {
	if len(c.cmd.Args) != 3 {
		return h.reply(ctx, c, "cmd.usage.review_timeout", nil)
	}
	d, ok := parseDuration(c.cmd.Args[1])
	fb, known := fallbacks[strings.ToLower(c.cmd.Args[2])]
	if !ok || !known {
		return h.reply(ctx, c, "cmd.usage.review_timeout", nil)
	}
	if h.d.Settings == nil {
		return h.unavailable(ctx, c)
	}
	mode, err := h.d.Settings.Get(ctx, c.group, "enforcement.mode")
	if err != nil && !errors.Is(err, ErrUnknownKey) {
		return fmt.Errorf("get enforcement mode: %w", err)
	}
	if errors.Is(review.CheckOnTimeout(review.EnforcementMode(mode), fb), review.ErrAutoBanInAskMode) {
		// A timeout must never become a ban the bot isn't allowed to make (§9.5).
		return h.reply(ctx, c, "cmd.review_timeout_ban_refused", nil)
	}
	return h.setAndReply(ctx, c, "review.timeout", humanDuration(d), "review.on_timeout", string(fb))
}

func humanDuration(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	default:
		return d.String()
	}
}

// reviewDecide handles .review K7Q2 ban: a vote by code. The review service checks that the
// voter is a current admin of the item's group.
func (h *Handler) reviewDecide(ctx context.Context, c *call) error {
	if h.d.Reviews == nil {
		return h.unavailable(ctx, c)
	}
	code := c.cmd.Args[0]
	decision, ok := review.ParseReply(strings.TrimSpace(strings.TrimPrefix(c.cmd.Rest, code)), true)
	if !ok || len(c.cmd.Args) < 2 {
		return h.reply(ctx, c, "cmd.usage.review", nil)
	}
	ref := c.m.Ref()
	b := review.Ballot{
		Code: code, Voter: c.person, VoterName: c.m.SenderName, Decision: decision, Method: review.MethodCommand,
		Raw: c.cmd.Rest, Chat: c.m.Chat, Message: &ref,
	}
	if c.where == inProtected {
		// Never acknowledge in a protected group: a late vote there is answered by DM below.
		b.Chat, b.Message = "", nil
	}
	res, err := h.d.Reviews.Vote(ctx, b)
	if errors.Is(err, review.ErrNotFound) {
		return h.reply(ctx, c, "cmd.review_not_found", map[string]string{"code": strings.ToUpper(code)})
	}
	if err != nil {
		return err
	}
	switch res.Outcome {
	case review.OutcomeDecided:
		return h.reply(ctx, c, "cmd.review_decided", map[string]string{
			"code": res.Item.Code, "decision": h.t(c, "review.decision."+string(res.Item.Decision), nil),
		})
	case review.OutcomeInvalid, review.OutcomeUnknown:
		return h.reply(ctx, c, "cmd.usage.review", nil)
	case review.OutcomeAlreadyHandled:
		if c.where == inProtected {
			return h.reply(ctx, c, "review.already_handled", map[string]string{"name": review.DeciderName(res.Item, h.d.Texts, c.lang), "code": res.Item.Code})
		}
		// Elsewhere the review service acknowledges late votes itself.
	case review.OutcomeNotAdmin, review.OutcomeWithdrawn:
		// Non-admins get nothing.
	}
	return nil
}

func (h *Handler) cmdPending(ctx context.Context, c *call) error {
	if h.d.Reviews == nil {
		return h.unavailable(ctx, c)
	}
	items, err := h.d.Reviews.Pending(ctx, c.group)
	if err != nil {
		return fmt.Errorf("pending reviews: %w", err)
	}
	if len(items) == 0 {
		return h.reply(ctx, c, "cmd.pending_none", nil)
	}
	lines := make([]string, 0, len(items))
	for _, it := range items {
		who := it.Subject.Name
		if who == "" && it.Subject.Phone != "" {
			who = review.MaskNumber("+" + it.Subject.Phone.User())
		}
		line := fmt.Sprintf("*#%s* · %s", it.Code, h.t(c, "review.kind."+string(it.Kind), nil))
		if who != "" {
			line += " · " + who
		}
		lines = append(lines, line)
	}
	return h.reply(ctx, c, "cmd.pending", map[string]string{"items": strings.Join(lines, "\n"), "n": strconv.Itoa(len(items))})
}

// cmdReviewGroup sets (.reviewgroup here [group name]) or clears (.reviewgroup off) the review
// group. Only a separate, non-protected group can be one (§9.5, §9.7).
func (h *Handler) cmdReviewGroup(ctx context.Context, c *call) error {
	if h.d.Settings == nil {
		return h.unavailable(ctx, c)
	}
	if len(c.cmd.Args) == 0 {
		return h.reply(ctx, c, "cmd.usage.reviewgroup", nil)
	}
	switch strings.ToLower(c.cmd.Args[0]) {
	case "here":
		if c.where == inProtected {
			return h.reply(ctx, c, "cmd.reviewgroup_protected", nil)
		}
		if c.where != inReviewGroup {
			return h.reply(ctx, c, "cmd.usage.reviewgroup", nil)
		}
		return h.reviewGroupHere(ctx, c)
	case "off":
		if c.group == "" {
			return h.pickGroup(ctx, c)
		}
		ok, err := h.set(ctx, c, "review.review_group", "")
		if err != nil || !ok {
			return err
		}
		if dest, err := h.d.Settings.Get(ctx, c.group, "review.destination"); err == nil && dest == string(review.DestinationGroup) {
			if ok, err := h.set(ctx, c, "review.destination", string(review.DestinationDashboard)); err != nil || !ok {
				return err
			}
		}
		return h.reply(ctx, c, "cmd.reviewgroup_off", map[string]string{"group": h.groupName(ctx, c.group)})
	}
	return h.reply(ctx, c, "cmd.usage.reviewgroup", nil)
}

// reviewGroupHere makes the current chat the review group of the sender's protected group (named
// after "here", or the only one they administer), and warns about members who aren't admins of
// that group, because they'd see the evidence.
func (h *Handler) reviewGroupHere(ctx context.Context, c *call) error {
	groups, err := h.adminGroups(ctx, c.person)
	if err != nil {
		return err
	}
	want := strings.TrimSpace(strings.TrimPrefix(c.cmd.Rest, c.cmd.Args[0]))
	// The numbers are those of .groups; a community isn't a group a chat can review.
	entries := listing(groups)
	e, ok := matchEntry(entries, want)
	target := e.GroupRef
	if want == "" && len(groups) == 1 {
		target, ok = groups[0], true
	} else if e.community {
		ok = false
	}
	if !ok {
		return h.reply(ctx, c, "cmd.reviewgroup_pick", map[string]string{"groups": h.groupList(c, entries), "prefix": h.d.Prefix})
	}
	c.group = target.JID
	if ok, err := h.set(ctx, c, "review.review_group", string(c.m.Chat)); err != nil || !ok {
		return err
	}
	if ok, err := h.set(ctx, c, "review.destination", string(review.DestinationGroup)); err != nil || !ok {
		return err
	}
	errs := []error{h.reply(ctx, c, "cmd.reviewgroup_set", map[string]string{"group": target.Name})}
	people, err := h.d.Groups.Participants(ctx, c.m.Chat)
	if err != nil {
		return errors.Join(append(errs, fmt.Errorf("participants of %s: %w", c.m.Chat, err))...)
	}
	outsiders := 0
	for _, p := range people {
		if p == h.d.Self {
			continue
		}
		admin, err := h.isAdmin(ctx, target.JID, p)
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		if !admin {
			outsiders++
		}
	}
	if outsiders > 0 {
		errs = append(errs, h.reply(ctx, c, "cmd.reviewgroup_outsiders", map[string]string{"n": strconv.Itoa(outsiders), "group": target.Name}))
	}
	return errors.Join(errs...)
}
