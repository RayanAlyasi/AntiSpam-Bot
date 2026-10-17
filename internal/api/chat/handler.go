package chat

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Windows for follow-ups to a command.
const (
	banReasonWindow   = 10 * time.Minute // a "spam"/"other" answer upgrades a .ban without a reason
	wordConfirmWindow = 10 * time.Minute // repeating .word block saves it
	reminderInterval  = 24 * time.Hour   // "bot is not admin" reminders per group
	maxJoinsPerDay    = 5                // invite-link joins per bot per day (§13.6)
)

// where a command was sent.
type where int

const (
	inDM where = iota + 1
	inProtected
	inReviewGroup
)

// call is one command invocation.
type call struct {
	m      Message
	cmd    Command
	person wa.JID // canonical sender
	group  wa.JID // the protected group the command acts on; empty if none could be chosen
	where  where
	lang   string
}

type dmContext struct {
	group   wa.JID
	expires time.Time
}

type pendingBan struct {
	group, target wa.JID
	msg           *wa.MessageRef
	text          string
	at            time.Time
}

type pendingWord struct {
	group  wa.JID
	phrase string
	at     time.Time
}

// Handler handles DMs, review-group messages and in-group commands.
type Handler struct {
	d    Deps
	gate *Gate

	mu        sync.Mutex
	contexts  map[string]dmContext   // person|chat → .use context
	bans      map[wa.JID]pendingBan  // admin → .ban waiting for a reason
	words     map[wa.JID]pendingWord // admin → .word block waiting for confirmation
	waiting   map[wa.JID][]string    // person → notices held until they open a thread
	reminders map[string]time.Time   // person|group → last not-admin reminder
	joins     []time.Time            // invite-link joins in the last day
}

var _ blacklist.Notifier = (*Handler)(nil)

// New returns a Handler.
func New(d Deps) *Handler {
	if d.Texts == nil {
		d.Texts = review.Texts
	}
	if d.Clock == nil {
		d.Clock = clock.Real{}
	}
	if d.Prefix == "" {
		d.Prefix = DefaultPrefix
	}
	if d.Language == "" {
		d.Language = "en"
	}
	if d.Threads == nil {
		d.Threads = NewMemoryThreads()
	}
	if d.Normalize == nil {
		d.Normalize = blacklist.SimpleNormalizer{}
	}
	return &Handler{
		d:         d,
		gate:      NewGate(d.Threads, d.Clock),
		contexts:  map[string]dmContext{},
		bans:      map[wa.JID]pendingBan{},
		words:     map[wa.JID]pendingWord{},
		waiting:   map[wa.JID][]string{},
		reminders: map[string]time.Time{},
	}
}

// Gate returns the DM gate, so other senders (reviews, notices) share the inbound-first rule.
func (h *Handler) Gate() *Gate { return h.gate }

func (h *Handler) now() time.Time { return h.d.Clock.Now() }

func (h *Handler) canonical(ctx context.Context, j wa.JID) wa.JID {
	if h.d.Identity == nil || j == "" {
		return j
	}
	c, err := h.d.Identity.Canonical(ctx, j)
	if err != nil || c == "" {
		return j
	}
	return c
}

func (h *Handler) isAdmin(ctx context.Context, group, person wa.JID) (bool, error) {
	if group == "" || person == "" {
		return false, nil
	}
	ok, err := h.d.Admins.IsAdmin(ctx, group, person)
	if err != nil {
		return false, fmt.Errorf("admin check %s: %w", group, err)
	}
	return ok, nil
}

// senderIsAdmin checks the sender under every JID we know for them.
func (h *Handler) senderIsAdmin(ctx context.Context, group wa.JID, m Message, person wa.JID) (bool, error) {
	seen := map[wa.JID]bool{}
	for _, j := range []wa.JID{person, m.Sender, m.SenderPhone} {
		if j == "" || seen[j] {
			continue
		}
		seen[j] = true
		ok, err := h.isAdmin(ctx, group, j)
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

// Handle processes one inbound message or reaction. Commands from anyone who isn't a current
// admin of the target group are ignored; nothing is ever posted in a protected group.
func (h *Handler) Handle(ctx context.Context, m Message) error {
	if m.Sender == "" || (h.d.Self != "" && (m.Sender == h.d.Self || m.SenderPhone == h.d.Self)) {
		return nil
	}
	if m.IsDM() {
		return h.handleDM(ctx, m)
	}
	protected, err := h.d.Groups.IsProtected(ctx, m.Chat)
	if err != nil {
		return fmt.Errorf("group kind %s: %w", m.Chat, err)
	}
	if protected {
		return h.handleProtected(ctx, m)
	}
	return h.handleOtherGroup(ctx, m)
}

// handleProtected acts on admin commands in a protected group: the command message is deleted
// and the reply goes by DM. Everything else (reactions, ordinary messages) isn't ours.
func (h *Handler) handleProtected(ctx context.Context, m Message) error {
	if m.Reaction != nil {
		return nil
	}
	cmd, ok := Parse(m.Text, h.d.Prefix)
	if !ok {
		return nil
	}
	person := h.canonical(ctx, m.Sender)
	admin, err := h.senderIsAdmin(ctx, m.Chat, m, person)
	if err != nil || !admin {
		return err
	}
	c := &call{m: m, cmd: cmd, person: person, group: m.Chat, where: inProtected}
	c.lang = h.lang(ctx, c)
	errs := []error{h.d.Actions.Enqueue(ctx, review.Delete{Group: m.Chat, Message: m.Ref(), Priority: review.P1, Reason: "command"})}
	errs = append(errs, h.dispatch(ctx, c))
	return errors.Join(errs...)
}

// handleOtherGroup handles a group the bot doesn't protect: a review group (votes and commands)
// or a group about to become one (.reviewgroup here). Anything else stays silent.
func (h *Handler) handleOtherGroup(ctx context.Context, m Message) error {
	served, err := h.d.Groups.ServedBy(ctx, m.Chat)
	if err != nil {
		return fmt.Errorf("review group lookup %s: %w", m.Chat, err)
	}
	person := h.canonical(ctx, m.Sender)
	if m.Reaction != nil {
		if h.d.Reviews == nil {
			return nil
		}
		_, _, err := h.d.Reviews.HandleReaction(ctx, m.Chat, person, m.Reaction.Target, m.Reaction.Emoji)
		return err
	}
	cmd, isCmd := Parse(m.Text, h.d.Prefix)
	if m.Quoted != nil && h.d.Reviews != nil {
		text := m.Text
		if isCmd && (cmd.Name == "spam" || cmd.Name == "notspam" || cmd.Name == "ok") {
			text = map[string]string{"spam": "ban", "notspam": "not spam", "ok": "ok"}[cmd.Name]
		}
		if !isCmd || text != m.Text {
			_, handled, err := h.d.Reviews.HandleReply(ctx, m.Chat, person, m.Ref(), m.Quoted.ID, text)
			if handled || err != nil {
				return err
			}
		}
	}
	if !isCmd || (len(served) == 0 && cmd.Name != "reviewgroup") {
		return nil
	}
	// Only admins of some protected group get any answer here: anyone else could otherwise make
	// the bot post on demand.
	mine, err := h.d.Groups.AdminGroups(ctx, person)
	if err != nil {
		return fmt.Errorf("groups of sender: %w", err)
	}
	if len(mine) == 0 {
		return nil
	}
	c := &call{m: m, cmd: cmd, person: person, where: inReviewGroup}
	isHere := cmd.Name == "reviewgroup" && len(cmd.Args) > 0 && strings.EqualFold(cmd.Args[0], "here")
	if !isHere {
		// .reviewgroup here picks its group from the sender's own groups, so a review group can
		// serve several protected groups.
		c.group = h.contextGroup(ctx, c, served)
	}
	if c.group != "" {
		admin, err := h.senderIsAdmin(ctx, c.group, m, person)
		if err != nil || !admin {
			// Members of a review group who aren't admins of the protected group are ignored.
			return err
		}
	}
	c.lang = h.lang(ctx, c)
	if c.group == "" && table[cmd.Name].needsGroup {
		return h.pickGroup(ctx, c)
	}
	return h.dispatch(ctx, c)
}

// handleDM records the open thread and routes the DM by intent.
func (h *Handler) handleDM(ctx context.Context, m Message) error {
	person := h.canonical(ctx, m.Sender)
	if err := h.gate.Inbound(ctx, person); err != nil {
		return err
	}
	if person != m.Sender {
		// The thread is open whichever JID the person used.
		if err := h.gate.Inbound(ctx, m.Sender); err != nil {
			return err
		}
	}
	errs := []error{h.flushWaiting(ctx, person, m.Chat)}
	if m.Reaction != nil {
		if h.d.Reviews != nil {
			_, _, err := h.d.Reviews.HandleReaction(ctx, m.Chat, person, m.Reaction.Target, m.Reaction.Emoji)
			errs = append(errs, err)
		}
		return errors.Join(errs...)
	}
	if cmd, ok := Parse(m.Text, h.d.Prefix); ok {
		c := &call{m: m, cmd: cmd, person: person, where: inDM}
		c.group = h.contextGroup(ctx, c, nil)
		c.lang = h.lang(ctx, c)
		return errors.Join(append(errs, h.dispatch(ctx, c))...)
	}
	if m.Quoted != nil && h.d.Reviews != nil {
		_, handled, err := h.d.Reviews.HandleReply(ctx, m.Chat, person, m.Ref(), m.Quoted.ID, m.Text)
		if handled || err != nil {
			return errors.Join(append(errs, err)...)
		}
	}
	text := strings.ToLower(strings.TrimSpace(m.Text))
	switch {
	case h.banReason(ctx, person, m, text, &errs):
	case text == "start" || text == "ابدأ" || text == "ابدا" || text == "بدء":
		errs = append(errs, h.start(ctx, m))
	case inviteLink.MatchString(m.Text):
		errs = append(errs, h.joinFromLink(ctx, m, person))
	}
	return errors.Join(errs...)
}

// contextGroup picks the protected group a command acts on: the group named in the review item
// being replied to, the .use context, the only group served or administered, in that order.
func (h *Handler) contextGroup(ctx context.Context, c *call, served []wa.JID) wa.JID {
	if c.m.Quoted != nil && h.d.Reviews != nil && c.where == inReviewGroup {
		// Store lookups go through the service: a reply quoting a review names its group.
		if it, err := h.reviewFor(ctx, c.m.Chat, c.m.Quoted.ID); err == nil {
			return it.Group
		}
	}
	key := string(c.person) + "|" + string(c.m.Chat)
	h.mu.Lock()
	dc, ok := h.contexts[key]
	h.mu.Unlock()
	if ok && h.now().Before(dc.expires) {
		return dc.group
	}
	if len(served) == 1 {
		return served[0]
	}
	if c.where == inDM {
		groups, err := h.d.Groups.AdminGroups(ctx, c.person)
		if err == nil && len(groups) == 1 {
			return groups[0].JID
		}
	}
	return ""
}

// reviewFor finds the review item a message of ours belongs to, through a reply-free lookup.
func (h *Handler) reviewFor(ctx context.Context, chat wa.JID, id wa.MessageID) (review.Item, error) {
	return h.d.Reviews.ItemForMessage(ctx, chat, id)
}

func (h *Handler) setContext(c *call, group wa.JID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.contexts[string(c.person)+"|"+string(c.m.Chat)] = dmContext{group: group, expires: h.now().Add(ContextTTL)}
}

// lang is the reply language: Arabic when the command used an Arabic alias, otherwise the
// group's review.language, otherwise the default.
func (h *Handler) lang(ctx context.Context, c *call) string {
	if c.cmd.Arabic {
		return "ar"
	}
	if c.group != "" && h.d.Settings != nil {
		if l, err := h.d.Settings.Get(ctx, c.group, "review.language"); err == nil && l != "" {
			return l
		}
	}
	return h.d.Language
}

func (h *Handler) t(c *call, key string, vars map[string]string) string {
	return h.d.Texts.T(c.lang, key, vars)
}

// reply answers a command where it came from: in the review group, or by DM (never in a
// protected group). A DM to someone without an open thread uses the once-a-day exception.
func (h *Handler) reply(ctx context.Context, c *call, key string, vars map[string]string) error {
	return h.replyText(ctx, c, h.t(c, key, vars))
}

func (h *Handler) replyText(ctx context.Context, c *call, text string) error {
	switch c.where {
	case inReviewGroup:
		ref := c.m.Ref()
		return h.d.Actions.Enqueue(ctx, review.Send{Chat: c.m.Chat, Message: wa.OutgoingMessage{Text: text, ReplyTo: &ref}, Purpose: "reply"})
	case inDM:
		return h.d.Actions.Enqueue(ctx, review.Send{Chat: c.m.Chat, Message: wa.OutgoingMessage{Text: text}, Purpose: "reply"})
	default:
		ok, err := h.gate.Allow(ctx, c.person, PurposeCommandReply)
		if err != nil || !ok {
			return err
		}
		return h.d.Actions.Enqueue(ctx, review.Send{Chat: c.m.Sender, Message: wa.OutgoingMessage{Text: text}, Purpose: "reply"})
	}
}

// Notify DMs an admin a notice if they have an open thread; otherwise it waits until they write.
// It implements blacklist.Notifier.
func (h *Handler) Notify(ctx context.Context, to, group wa.JID, key string, vars map[string]string) error {
	lang := h.d.Language
	if h.d.Settings != nil && group != "" {
		if l, err := h.d.Settings.Get(ctx, group, "review.language"); err == nil && l != "" {
			lang = l
		}
	}
	if vars == nil {
		vars = map[string]string{}
	}
	if _, ok := vars["group"]; !ok && group != "" {
		name, _ := h.d.Groups.Name(ctx, group)
		vars["group"] = name
	}
	return h.notice(ctx, h.canonical(ctx, to), to, h.d.Texts.T(lang, key, vars))
}

// notice sends text to person by DM when the thread is open, and holds it otherwise.
func (h *Handler) notice(ctx context.Context, person, chat wa.JID, text string) error {
	ok, err := h.gate.Allow(ctx, person, PurposeNotice)
	if err != nil {
		return err
	}
	if !ok {
		h.mu.Lock()
		if !slices.Contains(h.waiting[person], text) {
			h.waiting[person] = append(h.waiting[person], text)
		}
		h.mu.Unlock()
		return nil
	}
	return h.d.Actions.Enqueue(ctx, review.Send{Chat: chat, Message: wa.OutgoingMessage{Text: text}, Purpose: "notice"})
}

// flushWaiting delivers notices held for a person who just opened a thread.
func (h *Handler) flushWaiting(ctx context.Context, person, chat wa.JID) error {
	h.mu.Lock()
	held := h.waiting[person]
	delete(h.waiting, person)
	h.mu.Unlock()
	var errs []error
	for _, text := range held {
		errs = append(errs, h.d.Actions.Enqueue(ctx, review.Send{Chat: chat, Message: wa.OutgoingMessage{Text: text}, Purpose: "notice"}))
	}
	return errors.Join(errs...)
}

// audit writes an audit-log line for a command that changed something.
func (h *Handler) audit(ctx context.Context, c *call, action string, diff map[string]any) error {
	if h.d.Audit == nil {
		return nil
	}
	if diff == nil {
		diff = map[string]any{}
	}
	diff["via"] = "command"
	return h.d.Audit.Record(ctx, review.AuditEntry{Actor: c.person, Group: c.group, Action: action, Diff: diff, At: h.now()})
}

var inviteLink = regexp.MustCompile(`chat\.whatsapp\.com/(?:invite/)?([A-Za-z0-9]{16,32})`)
