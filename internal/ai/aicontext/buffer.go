package aicontext

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

// Buffer defaults: enough for the previous messages, the replied-to message and replies that
// arrive while a message waits for the AI, and nothing more (DESIGN.md §17: no normal-message
// text is kept beyond what running needs; this is memory only and gone on restart).
const (
	DefaultBufferPerChat = 100
	DefaultBufferMaxAge  = time.Hour
)

// Buffer keeps the most recent messages of each chat in memory. It implements Source and is safe
// for concurrent use. The pipeline adds every processed group message.
type Buffer struct {
	clock   clock.Clock
	perChat int
	maxAge  time.Duration

	mu    sync.Mutex
	chats map[string][]Msg // oldest first
}

var _ Source = (*Buffer)(nil)

// NewBuffer builds a buffer. Zero values use the defaults.
func NewBuffer(clk clock.Clock, perChat int, maxAge time.Duration) *Buffer {
	if clk == nil {
		clk = clock.Real{}
	}
	if perChat <= 0 {
		perChat = DefaultBufferPerChat
	}
	if maxAge <= 0 {
		maxAge = DefaultBufferMaxAge
	}
	return &Buffer{clock: clk, perChat: perChat, maxAge: maxAge, chats: map[string][]Msg{}}
}

// Add records a message. A message with an ID already present replaces it (an edit).
func (b *Buffer) Add(chat string, m Msg) {
	b.mu.Lock()
	defer b.mu.Unlock()
	msgs := b.prune(chat)
	if i := slices.IndexFunc(msgs, func(x Msg) bool { return x.ID == m.ID }); i >= 0 {
		msgs[i] = m
	} else {
		at, _ := slices.BinarySearchFunc(msgs, m.At, func(x Msg, t time.Time) int { return x.At.Compare(t) })
		msgs = slices.Insert(msgs, at, m)
	}
	if len(msgs) > b.perChat {
		msgs = slices.Delete(msgs, 0, len(msgs)-b.perChat)
	}
	b.chats[chat] = msgs
}

// Forget drops every buffered message of a chat (e.g. when the bot leaves the group).
func (b *Buffer) Forget(chat string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.chats, chat)
}

// Get implements Source.
func (b *Buffer) Get(_ context.Context, chat, id string) (Msg, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, m := range b.prune(chat) {
		if m.ID == id {
			return m, true, nil
		}
	}
	return Msg{}, false, nil
}

// Before implements Source.
func (b *Buffer) Before(_ context.Context, chat string, since, before time.Time, limit int) ([]Msg, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Msg
	for _, m := range b.prune(chat) {
		if !m.At.Before(since) && m.At.Before(before) {
			out = append(out, m)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// RepliesTo implements Source.
func (b *Buffer) RepliesTo(_ context.Context, chat, id string, limit int) ([]Msg, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Msg
	for _, m := range b.prune(chat) {
		if m.ReplyTo == id && (limit <= 0 || len(out) < limit) {
			out = append(out, m)
		}
	}
	return out, nil
}

// prune drops messages older than maxAge; the caller holds the lock.
func (b *Buffer) prune(chat string) []Msg {
	msgs := b.chats[chat]
	cut := b.clock.Now().Add(-b.maxAge)
	i := 0
	for i < len(msgs) && msgs[i].At.Before(cut) {
		i++
	}
	if i > 0 {
		msgs = slices.Delete(msgs, 0, i)
		if len(msgs) == 0 {
			delete(b.chats, chat)
		} else {
			b.chats[chat] = msgs
		}
	}
	return msgs
}

// Sweep drops expired messages in every chat, so a chat that went quiet doesn't keep text in
// memory past maxAge. Call it periodically (e.g. every few minutes).
func (b *Buffer) Sweep() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for chat := range b.chats {
		b.prune(chat)
	}
}

// Len returns the number of buffered messages in a chat.
func (b *Buffer) Len(chat string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.chats[chat])
}
