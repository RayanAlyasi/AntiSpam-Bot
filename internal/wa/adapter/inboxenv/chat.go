package inboxenv

import (
	"encoding/json"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter"
)

// Chat is what the command, DM and review handler needs from one inbox row: the text a command
// is parsed from, the message it replies to (with that message's text, for `.spam`), the
// mentions, or the reaction it is.
type Chat struct {
	Text     string
	Quoted   *ChatQuoted
	Mentions []wa.JID
	Reaction *ChatReaction
}

// ChatQuoted is the message a reply quotes.
type ChatQuoted struct {
	ID     wa.MessageID
	Sender wa.JID
	Text   string
}

// ChatReaction is a reaction; an empty Emoji removes it.
type ChatReaction struct {
	Target wa.MessageID
	Emoji  string
}

// DecodeChat reads the chat parts of one inbox row. Undecryptable rows have none.
func DecodeChat(kind string, raw []byte, infoJSON json.RawMessage, bot wa.JID) (Chat, error) {
	if kind == KindUndecryptable {
		return Chat{}, nil
	}
	if kind != KindMessage {
		return Chat{}, fmt.Errorf("%w: unknown kind %q", ErrCorrupt, kind)
	}
	var info types.MessageInfo
	if err := json.Unmarshal(infoJSON, &info); err != nil {
		return Chat{}, fmt.Errorf("%w: message info: %w", ErrCorrupt, err)
	}
	var msg waE2E.Message
	if err := proto.Unmarshal(raw, &msg); err != nil {
		return Chat{}, fmt.Errorf("%w: message %s: %w", ErrCorrupt, info.ID, err)
	}
	evt := (&events.Message{Info: info, Message: &msg, RawMessage: &msg}).UnwrapRaw()
	if r := evt.Message.GetReactionMessage(); r != nil {
		return Chat{Reaction: &ChatReaction{Target: wa.MessageID(r.GetKey().GetID()), Emoji: r.GetText()}}, nil
	}
	var c Chat
	if e := adapter.Build(evt, bot).Envelope; e != nil {
		c.Text = mainText(e)
		c.Mentions = e.Mentions
		if q := e.Quoted; q != nil && q.ID != "" {
			c.Quoted = &ChatQuoted{ID: q.ID, Sender: q.Sender}
		}
	}
	if c.Quoted != nil {
		if qm := quotedMessage(evt.Message); qm != nil {
			qinfo := info
			qinfo.ID = types.MessageID(c.Quoted.ID)
			if qe := adapter.Build(&events.Message{Info: qinfo, Message: qm}, bot).Envelope; qe != nil {
				c.Quoted.Text = allText(qe)
			}
		}
	}
	return c, nil
}

// mainText is the text a command is written in: the body, else a caption.
func mainText(e *envelope.Envelope) string {
	for _, src := range []string{"body", "caption"} {
		for _, t := range e.Texts {
			if t.Source == src && strings.TrimSpace(t.Raw) != "" {
				return t.Raw
			}
		}
	}
	return ""
}

func allText(e *envelope.Envelope) string {
	parts := make([]string, 0, len(e.Texts))
	for _, t := range e.Texts {
		if strings.TrimSpace(t.Raw) != "" {
			parts = append(parts, t.Raw)
		}
	}
	return strings.Join(parts, "\n")
}

// quotedMessage returns the quoted message from the first ContextInfo of the content.
func quotedMessage(m *waE2E.Message) *waE2E.Message {
	var found *waE2E.Message
	m.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind || fd.IsList() || fd.IsMap() {
			return true
		}
		ci := v.Message().Descriptor().Fields().ByName("contextInfo")
		if ci == nil || !v.Message().Has(ci) {
			return true
		}
		if c, ok := v.Message().Get(ci).Message().Interface().(*waE2E.ContextInfo); ok && c.GetQuotedMessage() != nil {
			found = c.GetQuotedMessage()
			return false
		}
		return true
	})
	return found
}
