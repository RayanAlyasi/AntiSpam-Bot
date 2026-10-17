// Package aicontext builds the AI context bundle (DESIGN.md §7.4, §17): the message the judged
// one replies to, the previous few messages in the chat, the group's name, topic and allowed
// content, sender context and existing replies. Everything is redacted with the shared helper,
// truncated, capped at about 2,000 characters (oldest context dropped first) and handed to the
// classifier, which renders it as fenced, JSON-encoded data. It reads only short-lived sources
// (the in-memory Buffer, or the 72 h inbox behind Source) and stores nothing.
package aicontext

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize/redact"
)

// Defaults (DESIGN.md §7.4).
const (
	DefaultPrevious   = 5                // N previous messages
	DefaultWindow     = 10 * time.Minute // how far back previous messages may be
	DefaultItemRunes  = 200              // each context message is truncated to this
	DefaultTotalRunes = 2000             // cap on all context text together
	MaxReplies        = 5
)

// NoContextHash is the context hash when the bundle is off, so cache keys stay stable.
const NoContextHash = "none"

// Msg is one message as a source holds it.
type Msg struct {
	ID         string
	Sender     string // opaque sender ID; never sent to the AI
	SenderName string // push name; never sent, and masked wherever it appears in other text
	Text       string // normalized text
	At         time.Time
	ReplyTo    string // ID of the message it replies to, "" if none
	Flagged    bool   // the bot flagged it
	IsStatus   bool   // a status (as quoted in a reply)
}

// Source is a read-only view of recent messages in a chat.
type Source interface {
	// Get returns one message by ID.
	Get(ctx context.Context, chat, id string) (Msg, bool, error)
	// Before returns up to limit messages with since <= At < before, oldest first.
	Before(ctx context.Context, chat string, since, before time.Time, limit int) ([]Msg, error)
	// RepliesTo returns up to limit messages replying to id, oldest first.
	RepliesTo(ctx context.Context, chat, id string, limit int) ([]Msg, error)
}

// Input describes the judged message and its group.
type Input struct {
	Enabled  bool   // the group's ai.context (default on; off for privacy-sensitive groups)
	Chat     string // the group JID
	Message  Msg    // the judged message
	Group    ai.Group
	Sender   ai.Sender
	Names    []string // more names to mask, e.g. the group's member names
	Region   string   // the group's default country, for reading national phone numbers
	Previous int      // N; 0 means DefaultPrevious
}

// Build returns the context bundle and its hash, or nil and NoContextHash when the bundle is off.
// The same inputs always give the same bundle and hash.
func Build(ctx context.Context, src Source, in Input) (*ai.Context, string, error) {
	if !in.Enabled || src == nil {
		return nil, NoContextHash, nil
	}
	n := in.Previous
	if n <= 0 {
		n = DefaultPrevious
	}
	var replyTo *Msg
	if in.Message.ReplyTo != "" {
		m, ok, err := src.Get(ctx, in.Chat, in.Message.ReplyTo)
		if err != nil {
			return nil, "", fmt.Errorf("context: replied-to message: %w", err)
		}
		if ok {
			replyTo = &m
		}
	}
	prev, err := src.Before(ctx, in.Chat, in.Message.At.Add(-DefaultWindow), in.Message.At, n+2)
	if err != nil {
		return nil, "", fmt.Errorf("context: previous messages: %w", err)
	}
	prev = slices.DeleteFunc(prev, func(m Msg) bool { return m.ID == in.Message.ID || m.ID == in.Message.ReplyTo })
	if len(prev) > n {
		prev = prev[len(prev)-n:]
	}
	replies, err := src.RepliesTo(ctx, in.Chat, in.Message.ID, MaxReplies)
	if err != nil {
		return nil, "", fmt.Errorf("context: replies: %w", err)
	}

	// Mask every person's name the bundle could carry: the configured ones and the senders of
	// all messages in it, including the judged message's sender.
	names := slices.Clone(in.Names)
	add := func(m Msg) {
		if m.SenderName != "" {
			names = append(names, m.SenderName)
		}
	}
	add(in.Message)
	if replyTo != nil {
		add(*replyTo)
	}
	for _, m := range prev {
		add(m)
	}
	for _, m := range replies {
		add(m)
	}
	opts := redact.Options{Region: in.Region, Names: names, MaxRunes: DefaultItemRunes}
	red := func(s string) string { return redact.Text(s, opts) }
	cm := func(m Msg) ai.ContextMessage {
		return ai.ContextMessage{Text: red(m.Text), Flagged: m.Flagged, IsStatus: m.IsStatus}
	}

	c := &ai.Context{
		Group:  ai.Group{Name: red(in.Group.Name), Topic: red(in.Group.Topic), Allowed: red(in.Group.Allowed)},
		Sender: in.Sender,
	}
	if replyTo != nil {
		r := cm(*replyTo)
		c.ReplyTo = &r
	}
	for _, m := range prev {
		c.Previous = append(c.Previous, cm(m))
	}
	for _, m := range replies {
		c.Replies = append(c.Replies, cm(m))
	}
	capTotal(c, DefaultTotalRunes)

	raw, err := json.Marshal(c)
	if err != nil {
		return nil, "", fmt.Errorf("context: hash: %w", err)
	}
	sum := sha256.Sum256(raw)
	return c, hex.EncodeToString(sum[:]), nil
}

// Size is the number of runes of context text in c.
func Size(c *ai.Context) int {
	if c == nil {
		return 0
	}
	n := utf8.RuneCountInString(c.Group.Name) + utf8.RuneCountInString(c.Group.Topic) + utf8.RuneCountInString(c.Group.Allowed)
	if c.ReplyTo != nil {
		n += utf8.RuneCountInString(c.ReplyTo.Text)
	}
	for _, m := range c.Previous {
		n += utf8.RuneCountInString(m.Text)
	}
	for _, m := range c.Replies {
		n += utf8.RuneCountInString(m.Text)
	}
	return n
}

// capTotal drops the oldest context until the text fits: previous messages oldest first, then the
// replied-to message (it precedes the judged one), then the replies oldest first. The group fields
// are shortened last; with the default caps (200 runes per field) they never need to be.
func capTotal(c *ai.Context, limit int) {
	for Size(c) > limit && len(c.Previous) > 0 {
		c.Previous = c.Previous[1:]
	}
	if Size(c) > limit {
		c.ReplyTo = nil
	}
	for Size(c) > limit && len(c.Replies) > 0 {
		c.Replies = c.Replies[1:]
	}
	for _, f := range []*string{&c.Group.Allowed, &c.Group.Topic, &c.Group.Name} {
		if over := Size(c) - limit; over > 0 {
			*f = truncRunes(*f, max(0, utf8.RuneCountInString(*f)-over))
		}
	}
	if len(c.Previous) == 0 {
		c.Previous = nil
	}
	if len(c.Replies) == 0 {
		c.Replies = nil
	}
}

func truncRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
