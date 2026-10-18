// Package synth builds synthetic whatsmeow message events from a whatsmeow-free description,
// so tests and the replay tool can feed constructed messages through the real envelope builder
// (DESIGN.md §18) without importing whatsmeow themselves. It lives under internal/wa because
// only that tree may import whatsmeow. Nothing here talks to WhatsApp.
package synth

import (
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter"
)

// Spec describes one incoming group message.
type Spec struct {
	Chat     wa.JID       // the group
	Sender   wa.JID       // the sender's LID (or phone-number JID for PN-addressed groups)
	SenderPN wa.JID       // the sender's phone-number JID, when the server shares it
	ID       wa.MessageID //
	Time     time.Time    //
	PushName string       //
	Text     string       // the body; with VCards it becomes the contacts' display name
	VCards   []string     // raw vCard documents: one is a contact card, several a contacts array
}

// Event builds the whatsmeow event the connector would receive for s.
func Event(s Spec) (*events.Message, error) {
	chat, err := adapter.ToWM(s.Chat)
	if err != nil {
		return nil, fmt.Errorf("chat %q: %w", s.Chat, err)
	}
	sender, err := adapter.ToWM(s.Sender)
	if err != nil {
		return nil, fmt.Errorf("sender %q: %w", s.Sender, err)
	}
	src := types.MessageSource{Chat: chat, Sender: sender, IsGroup: s.Chat.IsGroup(), AddressingMode: types.AddressingModePN}
	if s.Sender.IsLID() {
		src.AddressingMode = types.AddressingModeLID
	}
	if s.SenderPN != "" {
		if src.SenderAlt, err = adapter.ToWM(s.SenderPN); err != nil {
			return nil, fmt.Errorf("sender phone number %q: %w", s.SenderPN, err)
		}
	}
	msg := message(s)
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: src,
			ID:            types.MessageID(s.ID),
			Type:          "text",
			PushName:      s.PushName,
			Timestamp:     s.Time,
		},
		Message:    msg,
		RawMessage: msg,
	}, nil
}

func message(s Spec) *waE2E.Message {
	switch len(s.VCards) {
	case 0:
		return &waE2E.Message{Conversation: proto.String(s.Text)}
	case 1:
		name := s.Text
		if name == "" {
			name = "Contact"
		}
		return &waE2E.Message{ContactMessage: &waE2E.ContactMessage{DisplayName: proto.String(name), Vcard: proto.String(s.VCards[0])}}
	default:
		cards := make([]*waE2E.ContactMessage, len(s.VCards))
		for i, v := range s.VCards {
			cards[i] = &waE2E.ContactMessage{DisplayName: proto.String(fmt.Sprintf("Contact %d", i+1)), Vcard: proto.String(v)}
		}
		return &waE2E.Message{ContactsArrayMessage: &waE2E.ContactsArrayMessage{DisplayName: proto.String(s.Text), Contacts: cards}}
	}
}

// Envelope builds s through the real envelope builder, as the bot would see it. It returns an
// error for a message the builder finds nothing to analyze in.
func Envelope(s Spec, bot wa.JID) (*envelope.Envelope, error) {
	evt, err := Event(s)
	if err != nil {
		return nil, err
	}
	res := adapter.Build(evt, bot)
	if res.Envelope == nil {
		return nil, fmt.Errorf("message %s: nothing to analyze", s.ID)
	}
	return res.Envelope, nil
}
