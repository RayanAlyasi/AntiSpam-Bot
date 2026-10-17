package adapter

import (
	"slices"
	"testing"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
)

func fp(m *waE2E.Message) *waE2E.FutureProofMessage { return &waE2E.FutureProofMessage{Message: m} }

func TestUnwrap(t *testing.T) {
	text := &waE2E.Message{Conversation: proto.String("hi")}
	tests := []struct {
		name      string
		msg       *waE2E.Message
		wrappers  []string
		layers    int
		editOf    string
		truncated bool
	}{
		{"plain", text, nil, 1, "", false},
		{"nil", nil, nil, 0, "", false},
		{"group mention", &waE2E.Message{GroupMentionedMessage: fp(text)}, []string{"GroupMentionedMessage"}, 1, "", false},
		{
			"spoiler in group status", &waE2E.Message{GroupStatusMessage: fp(&waE2E.Message{SpoilerMessage: fp(text)})},
			[]string{"GroupStatusMessage", "SpoilerMessage"},
			1, "", false,
		},
		{"device sent", &waE2E.Message{DeviceSentMessage: &waE2E.DeviceSentMessage{Message: text}}, []string{WrapperDeviceSent}, 1, "", false},
		{"edit", &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
			Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), Key: &waCommon.MessageKey{ID: proto.String("O")}, EditedMessage: text,
		}}, []string{WrapperEdit}, 1, "O", false},
		{
			"metadata only is not content", &waE2E.Message{MessageContextInfo: &waE2E.MessageContextInfo{}, AssociatedChildMessage: fp(text)},
			[]string{"AssociatedChildMessage"},
			1, "", false,
		},
		{
			"content beside a wrapper", &waE2E.Message{Conversation: proto.String("outer"), SpoilerMessage: fp(text)},
			[]string{"SpoilerMessage"},
			2, "", false,
		},
		{"empty wrapper", &waE2E.Message{SpoilerMessage: fp(nil)}, []string{"SpoilerMessage"}, 0, "", false},
		{"at the depth cap", nest(MaxDepth, text), repeat("SpoilerMessage", MaxDepth), 1, "", false},
		{"past the depth cap", nest(MaxDepth+3, text), repeat("SpoilerMessage", MaxDepth+1), 0, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := Unwrap(tt.msg)
			if !slices.Equal(u.Wrappers, tt.wrappers) || len(u.Layers) != tt.layers || u.EditOf != tt.editOf || u.Truncated != tt.truncated {
				t.Errorf("Unwrap = wrappers %v, %d layers, editOf %q, truncated %v; want %v, %d, %q, %v",
					u.Wrappers, len(u.Layers), u.EditOf, u.Truncated, tt.wrappers, tt.layers, tt.editOf, tt.truncated)
			}
		})
	}
}

func TestUnwrapCycleAndWidth(t *testing.T) {
	// A cyclic Go value can't come off the wire, but must not hang the unwrapper either.
	loop := &waE2E.Message{}
	loop.SpoilerMessage = fp(loop)
	if u := Unwrap(loop); !u.Truncated {
		t.Error("a cyclic message must stop at the caps and report Truncated")
	}
	e := Build(event(nest(MaxDepth+1, &waE2E.Message{Conversation: proto.String("hidden")})), bot).Envelope
	if e == nil || e.Wrappers[len(e.Wrappers)-1] != envelope.WrapperTruncated {
		t.Errorf("too-deep nesting = %+v, want an envelope whose Wrappers end in %q (evasion by nesting must be visible)", e, envelope.WrapperTruncated)
	}
	unset := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Key: &waCommon.MessageKey{ID: proto.String("X")}}}
	if res := Build(event(unset), bot); res.Revoke != nil {
		t.Error("a ProtocolMessage without a type must not be read as a revoke (REVOKE is the zero value)")
	}

	// A wide tree: every wrapper field set, each nesting more wrappers.
	wide := wideTree(4)
	if u := Unwrap(wide); !u.Truncated || len(u.Layers) > MaxLayers {
		t.Errorf("wide tree: %d layers, truncated %v; want at most %d layers and Truncated", len(u.Layers), u.Truncated, MaxLayers)
	}
}

func nest(n int, inner *waE2E.Message) *waE2E.Message {
	for range n {
		inner = &waE2E.Message{SpoilerMessage: fp(inner)}
	}
	return inner
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func wideTree(depth int) *waE2E.Message {
	m := &waE2E.Message{Conversation: proto.String("x")}
	if depth == 0 {
		return m
	}
	m.SpoilerMessage = fp(wideTree(depth - 1))
	m.GroupMentionedMessage = fp(wideTree(depth - 1))
	m.StatusMentionMessage = fp(wideTree(depth - 1))
	return m
}

// FuzzUnwrap feeds arbitrary bytes as a waE2E.Message: crafted nesting must never panic or loop,
// and building the envelope from it must not either.
func FuzzUnwrap(f *testing.F) {
	for _, m := range []*waE2E.Message{
		nest(8, &waE2E.Message{Conversation: proto.String("x")}),
		wideTree(3),
		{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), EditedMessage: nest(3, &waE2E.Message{})}},
		{ExtendedTextMessage: &waE2E.ExtendedTextMessage{ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("q"), QuotedMessage: nest(4, &waE2E.Message{})}}},
	} {
		b, err := proto.Marshal(m)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var m waE2E.Message
		if proto.Unmarshal(data, &m) != nil {
			return
		}
		u := Unwrap(&m)
		if len(u.Layers) > MaxLayers {
			t.Fatalf("%d layers, cap is %d", len(u.Layers), MaxLayers)
		}
		Build(event(&m), bot)
	})
}
