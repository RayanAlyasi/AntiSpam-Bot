package adapter

import (
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Limits of the unwrapper. Real messages nest up to five wrappers (device-sent, ephemeral,
// view-once, then a FutureProof wrapper or two); the caps only stop crafted nesting
// (DESIGN.md §5.2).
const (
	MaxDepth  = 8  // wrappers removed along one path, counting those UnwrapRaw also strips
	MaxLayers = 32 // messages visited in total, so wide trees stay cheap too
)

// Wrapper names recorded for wrappers that aren't FutureProofMessage fields.
const (
	WrapperDeviceSent = "DeviceSentMessage"
	WrapperEdit       = "ProtocolMessage.EditedMessage"
)

var (
	futureProofName = (&waE2E.FutureProofMessage{}).ProtoReflect().Descriptor().FullName()
	deviceSentName  = (&waE2E.DeviceSentMessage{}).ProtoReflect().Descriptor().FullName()
)

// Unwrapped is a message with its wrappers removed.
type Unwrapped struct {
	// Layers are the messages in the wrapper tree that carry content of their own, outermost
	// first. A normal message has exactly one; a crafted one may hide content beside a wrapper,
	// so every layer is analyzed.
	Layers []*waE2E.Message
	// Wrappers names each wrapper removed, outermost first, by its waE2E.Message field name
	// ("GroupStatusMessageV2"), WrapperDeviceSent or WrapperEdit.
	Wrappers []string
	// EditOf is the original message ID when an edit (ProtocolMessage MESSAGE_EDIT) was unwrapped.
	EditOf string
	// Truncated is true when a cap stopped the unwrapper: the message nests deeper than any
	// real client does.
	Truncated bool
}

// Unwrap removes every FutureProofMessage wrapper (GroupMentioned, Spoiler, StatusMention,
// GroupStatus, the ephemeral and view-once wrappers UnwrapRaw also strips, and any new one,
// found by reflection), DeviceSentMessage, and the EditedMessage of a MESSAGE_EDIT
// ProtocolMessage. It never panics and visits at most MaxLayers messages, so crafted nesting
// (even a cyclic Go value) can't make it loop.
func Unwrap(msg *waE2E.Message) Unwrapped {
	var u Unwrapped
	visited := 0
	u.walk(msg, 0, &visited)
	return u
}

func (u *Unwrapped) walk(msg *waE2E.Message, depth int, visited *int) {
	if msg == nil {
		return
	}
	if *visited >= MaxLayers {
		u.Truncated = true
		return
	}
	*visited++

	type inner struct {
		name string
		msg  *waE2E.Message
	}
	var inners []inner
	content := false
	m := msg.ProtoReflect()
	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if !m.Has(fd) {
			continue
		}
		switch {
		case fd.Message() != nil && fd.Message().FullName() == futureProofName:
			fp, _ := m.Get(fd).Message().Interface().(*waE2E.FutureProofMessage)
			inners = append(inners, inner{fieldName(fd), fp.GetMessage()})
		case fd.Message() != nil && fd.Message().FullName() == deviceSentName:
			inners = append(inners, inner{WrapperDeviceSent, msg.GetDeviceSentMessage().GetMessage()})
		case fd.Name() == "protocolMessage" && isEdit(msg.GetProtocolMessage()):
			if u.EditOf == "" {
				u.EditOf = msg.GetProtocolMessage().GetKey().GetID()
			}
			inners = append(inners, inner{WrapperEdit, msg.GetProtocolMessage().GetEditedMessage()})
		case fd.Name() == "messageContextInfo":
			// Metadata, not content.
		default:
			content = true
		}
	}
	if content {
		u.Layers = append(u.Layers, msg)
	}
	for _, in := range inners {
		u.Wrappers = append(u.Wrappers, in.name)
		if depth+1 > MaxDepth {
			u.Truncated = true
			continue
		}
		u.walk(in.msg, depth+1, visited)
	}
}

func isEdit(pm *waE2E.ProtocolMessage) bool {
	return pm.GetType() == waE2E.ProtocolMessage_MESSAGE_EDIT && pm.GetEditedMessage() != nil
}

// fieldName turns a proto field name ("groupStatusMessageV2") into the Go field name.
func fieldName(fd protoreflect.FieldDescriptor) string {
	n := string(fd.Name())
	if n == "" {
		return n
	}
	return strings.ToUpper(n[:1]) + n[1:]
}
