// Package inboxenv turns a stored inbox row back into the envelope the pipeline analyzes
// (DESIGN.md §5.1, §4 "replayable inbox"): the row holds the still-wrapped waE2E.Message proto
// and whatsmeow's MessageInfo as JSON, exactly as the connector received them (connector.convert),
// so the real envelope builder runs on them unchanged, live or in a replay.
package inboxenv

import (
	"encoding/json"
	"errors"
	"fmt"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter"
)

// Row kinds, as the inbox stores them (inbox.KindMessage, inbox.KindUndecryptable).
const (
	KindMessage       = "message"
	KindUndecryptable = "undecryptable"
)

// ErrCorrupt means the row can't be decoded: retrying won't help.
var ErrCorrupt = errors.New("inboxenv: corrupt inbox row")

// Result is what one inbox row becomes.
type Result struct {
	// Envelope is the content to analyze; nil when there is none (a reaction, a poll vote, a
	// key distribution, a plain revoke).
	Envelope *envelope.Envelope
	// Revoke is set when the row is the sender or an admin deleting a message.
	Revoke *wa.Revoke
	// Unknown lists message fields the envelope builder doesn't know (read by reflection), so a
	// new WhatsApp message type shows up in the logs (DESIGN.md §6.13). Field names only, no text.
	Unknown []string
}

// Decode builds the envelope of one inbox row. bot is the bot number that received it.
func Decode(kind string, raw []byte, infoJSON json.RawMessage, bot wa.JID) (Result, error) {
	var info types.MessageInfo
	if err := json.Unmarshal(infoJSON, &info); err != nil {
		return Result{}, fmt.Errorf("%w: message info: %v", ErrCorrupt, err)
	}
	switch kind {
	case KindUndecryptable:
		return Result{Envelope: adapter.BuildUndecryptable(&events.UndecryptableMessage{Info: info}, bot)}, nil
	case KindMessage:
		var msg waE2E.Message
		if err := proto.Unmarshal(raw, &msg); err != nil {
			return Result{}, fmt.Errorf("%w: message %s: %v", ErrCorrupt, info.ID, err)
		}
		evt := (&events.Message{Info: info, Message: &msg, RawMessage: &msg}).UnwrapRaw()
		res := adapter.Build(evt, bot)
		return Result{Envelope: res.Envelope, Revoke: res.Revoke, Unknown: res.Unknown}, nil
	default:
		return Result{}, fmt.Errorf("%w: unknown kind %q", ErrCorrupt, kind)
	}
}
