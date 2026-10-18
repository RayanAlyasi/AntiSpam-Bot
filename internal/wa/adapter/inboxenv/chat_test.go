package inboxenv

import (
	"encoding/json"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func row(t *testing.T, msg *waE2E.Message) ([]byte, json.RawMessage) {
	t.Helper()
	chat, _ := types.ParseJID("120363000000000001@g.us")
	sender, _ := types.ParseJID("200@lid")
	info := types.MessageInfo{MessageSource: types.MessageSource{Chat: chat, Sender: sender, IsGroup: true}, ID: "R1", Timestamp: time.Unix(1_700_000_000, 0)}
	raw, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	js, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	return raw, js
}

func TestDecodeChat(t *testing.T) {
	reply := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text: proto.String(".spam"),
		ContextInfo: &waE2E.ContextInfo{
			StanzaID:      proto.String("SPAM1"),
			Participant:   proto.String("300@lid"),
			QuotedMessage: &waE2E.Message{Conversation: proto.String("win crypto money now")},
		},
	}}
	raw, info := row(t, reply)
	c, err := DecodeChat(KindMessage, raw, info, "100@lid")
	if err != nil {
		t.Fatal(err)
	}
	if c.Text != ".spam" || c.Quoted == nil || c.Quoted.ID != "SPAM1" || c.Quoted.Sender != "300@lid" || c.Quoted.Text != "win crypto money now" {
		t.Fatalf("reply decoded as %+v / %+v; want the command, the quoted ID, sender and text (`.spam` learns from it)", c, c.Quoted)
	}

	react := &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: &waCommon.MessageKey{ID: proto.String("REV1")}, Text: proto.String("✅")}}
	raw, info = row(t, react)
	c, err = DecodeChat(KindMessage, raw, info, "100@lid")
	if err != nil || c.Reaction == nil || c.Reaction.Target != "REV1" || c.Reaction.Emoji != "✅" {
		t.Fatalf("reaction decoded as %+v, %v; want ✅ on REV1 (a review vote)", c.Reaction, err)
	}

	removed := &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: &waCommon.MessageKey{ID: proto.String("REV1")}, Text: proto.String("")}}
	raw, info = row(t, removed)
	if c, err = DecodeChat(KindMessage, raw, info, "100@lid"); err != nil || c.Reaction == nil || c.Reaction.Emoji != "" {
		t.Fatalf("removed reaction decoded as %+v, %v; want an empty emoji", c.Reaction, err)
	}

	plain := &waE2E.Message{Conversation: proto.String(".mode shadow")}
	raw, info = row(t, plain)
	if c, err = DecodeChat(KindMessage, raw, info, "100@lid"); err != nil || c.Text != ".mode shadow" || c.Quoted != nil {
		t.Fatalf("plain command decoded as %+v, %v", c, err)
	}
}
