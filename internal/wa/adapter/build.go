// The envelope builder turns whatsmeow message events into Content Envelopes (DESIGN.md
// §5.3). It is the only code that reads waE2E protobufs; analyzers only ever see envelopes.

package adapter

import (
	"slices"
	"strings"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// BuildResult is what one message event becomes.
type BuildResult struct {
	// Envelope is the message's content. It's nil for messages with nothing to analyze, such as
	// a plain revoke (reactions, poll votes, key distribution and other protocol messages).
	Envelope *envelope.Envelope
	// Revoke is set when the server marks the message as a sender or admin revoke.
	Revoke *wa.Revoke
	// Unknown lists the waE2E.Message fields read by reflection because the builder doesn't
	// know them (DESIGN.md §6.13). Count them in the unknown-type metric to notice new vectors.
	Unknown []string
	// Truncated is true when a cap cut the message short: wrapper nesting or contact cards.
	Truncated bool
}

// Build builds the envelope for a received message. bot is the bot's own JID.
func Build(evt *events.Message, bot wa.JID) BuildResult {
	// The raw message, when there is one, so every wrapper (including those UnwrapRaw strips,
	// such as ephemeral and view-once) is recorded the same way.
	msg := evt.Message
	if evt.RawMessage != nil {
		msg = evt.RawMessage
	}
	u := Unwrap(msg)
	info := evt.Info

	// Only the server's edit attribute makes a message a revoke: a REVOKE ProtocolMessage in the
	// content alone is unattested and must not cancel actions. Any other content in the message
	// is still analyzed below.
	var rev *wa.Revoke
	if info.Edit == types.EditAttributeSenderRevoke || info.Edit == types.EditAttributeAdminRevoke {
		rev = revoke(info, u)
	}

	b := newBuilder(0)
	b.env.Bot = bot
	b.env.Group = FromWM(info.Chat)
	b.env.Sender = sender(info)
	b.env.MsgID = wa.MessageID(info.ID)
	b.env.IsViewOnce = evt.IsViewOnce
	b.env.IsEdit = evt.IsEdit || u.EditOf != ""
	if u.EditOf != "" {
		b.env.MsgID = wa.MessageID(u.EditOf)
	}
	b.wrappers(u)
	for _, layer := range u.Layers {
		b.layer(layer)
	}
	b.selfContacts()
	truncated := u.Truncated || b.truncated
	if truncated {
		b.env.Wrappers = append(b.env.Wrappers, envelope.WrapperTruncated)
	}
	res := BuildResult{Revoke: rev, Unknown: b.unknown, Truncated: truncated}
	if b.hasContent() || truncated {
		b.finish()
		res.Envelope = b.env
	}
	return res
}

// BuildUndecryptable builds the content-less envelope for a message the bot couldn't decrypt,
// including view-once media, which linked devices never receive (DESIGN.md §6.15). Policy and
// sender reputation still apply, and the message can still be deleted by its ID.
func BuildUndecryptable(evt *events.UndecryptableMessage, bot wa.JID) *envelope.Envelope {
	return &envelope.Envelope{
		Bot:        bot,
		Group:      FromWM(evt.Info.Chat),
		Sender:     sender(evt.Info),
		MsgID:      wa.MessageID(evt.Info.ID),
		Kind:       envelope.KindUndecryptable,
		IsViewOnce: evt.UnavailableType == events.UnavailableTypeViewOnce,
	}
}

// sender keys the person by LID and adds the phone number when the message carries it
// (DESIGN.md §7.7).
func sender(info types.MessageInfo) envelope.Identity {
	id := envelope.Identity{PushName: info.PushName}
	for _, j := range []types.JID{info.Sender, info.SenderAlt} {
		switch j.Server {
		case types.HiddenUserServer:
			if id.LID == "" {
				id.LID = FromWM(j)
			}
		case types.DefaultUserServer:
			if id.PN == "" {
				id.PN = FromWM(j)
			}
		}
	}
	if info.VerifiedName != nil && info.VerifiedName.Details != nil {
		id.VerifiedName = info.VerifiedName.Details.GetVerifiedName()
	}
	return id
}

// revoke reads the deleted message's key. The edit attribute is set by the sending client and
// the key travels encrypted, so neither is checked by the server: a sender revoke must name the
// sender's own message (as LID or phone number), or it's ignored, since clients wouldn't delete
// someone else's message for it. A revoke without a REVOKE ProtocolMessage and an ID is ignored.
func revoke(info types.MessageInfo, u Unwrapped) *wa.Revoke {
	var key *waCommon.MessageKey
	for _, l := range u.Layers {
		pm := l.GetProtocolMessage()
		if pm.Type != nil && pm.GetType() == waE2E.ProtocolMessage_REVOKE && pm.GetKey().GetID() != "" {
			key = pm.GetKey()
			break
		}
	}
	if key == nil {
		return nil
	}
	r := &wa.Revoke{
		Chat:    FromWM(info.Chat),
		Target:  wa.MessageRef{ID: wa.MessageID(key.GetID()), Sender: parseJID(key.GetParticipant())},
		By:      FromWM(info.Sender),
		ByAdmin: info.Edit == types.EditAttributeAdminRevoke,
	}
	if r.ByAdmin {
		return r
	}
	switch r.Target.Sender {
	case "", r.By, FromWM(info.SenderAlt):
		r.Target.Sender = r.By
		return r
	}
	return nil
}

// parseJID converts a JID string from a protobuf, dropping its device part.
func parseJID(s string) wa.JID {
	if s == "" {
		return ""
	}
	j, err := types.ParseJID(s)
	if err != nil {
		return wa.JID(s)
	}
	return FromWM(j)
}

// builder fills one envelope.
type builder struct {
	env       *envelope.Envelope
	unknown   []string
	nested    int  // depth of nested builds (quoted messages, payment notes)
	truncated bool // a cap dropped content
}

// maxNested bounds builds inside builds: a quoted message quoting another, and so on.
const maxNested = 2

func newBuilder(nested int) *builder {
	return &builder{env: &envelope.Envelope{}, nested: nested}
}

// kind sets the envelope's kind. Text is the weakest: a crafted message with text and a video
// is a video, so per-kind policies ("videos not allowed") can't be dodged by adding text.
func (b *builder) kind(k envelope.Kind) {
	if b.env.Kind == "" || (b.env.Kind == envelope.KindText && k != envelope.KindText) {
		b.env.Kind = k
	}
}

func (b *builder) text(source, s string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	b.env.Texts = append(b.env.Texts, envelope.TextSurface{Source: source, Raw: s})
}

func (b *builder) url(source, u string) {
	u = strings.TrimSpace(u)
	if u == "" {
		return
	}
	for _, have := range b.env.URLs {
		if have.URL == u && have.Source == source {
			return
		}
	}
	b.env.URLs = append(b.env.URLs, envelope.URLRef{URL: u, Source: source})
}

func (b *builder) thumb(kind envelope.Kind, thumb []byte) {
	if len(thumb) > 0 {
		b.env.Media = append(b.env.Media, envelope.MediaRef{Type: kind, Thumb: thumb})
	}
}

func (b *builder) hasContent() bool {
	e := b.env
	return e.Kind != "" || len(e.Texts) > 0 || len(e.URLs) > 0 || len(e.Media) > 0 || len(e.Contacts) > 0 ||
		len(e.Channels) > 0 || len(e.Invites) > 0 || e.Quoted != nil || e.Status != nil || len(b.unknown) > 0
}

func (b *builder) finish() {
	if b.env.Kind == "" {
		b.env.Kind = envelope.KindUnknown
	}
}

func (b *builder) status() *envelope.StatusRef {
	if b.env.Status == nil {
		b.env.Status = &envelope.StatusRef{}
	}
	return b.env.Status
}

// wrappers records the removed wrappers, and the status context some of them carry.
func (b *builder) wrappers(u Unwrapped) {
	b.env.Wrappers = slices.Clone(u.Wrappers)
	for _, w := range u.Wrappers {
		switch w {
		case "StatusMentionMessage", "GroupStatusMentionMessage":
			b.status().IsStatusMention = true
		case "GroupStatusMessage", "GroupStatusMessageV2":
			b.status().IsGroupStatus = true
		case "ViewOnceMessage", "ViewOnceMessageV2", "ViewOnceMessageV2Extension":
			b.env.IsViewOnce = true
		}
	}
}

// layer reads every populated field of one unwrapped message: known content through its
// handler, unknown fields by reflection, and each part's ContextInfo.
func (b *builder) layer(msg *waE2E.Message) {
	m := msg.ProtoReflect()
	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if !m.Has(fd) {
			continue
		}
		name := string(fd.Name())
		if h, ok := handlers[name]; ok {
			h(b, msg)
		} else if g, ok := generics[name]; ok {
			b.kind(g.kind)
			b.reflect(m.Get(fd), g.source, 0)
		} else if _, ok := ignored[name]; ok || isWrapperField(fd) {
			continue
		} else {
			b.unknown = append(b.unknown, fieldName(fd))
			b.reflect(m.Get(fd), "unknown", 0)
		}
		if fd.Message() != nil && !fd.IsList() && !fd.IsMap() {
			if ci, ok := contextInfo(m.Get(fd).Message()); ok {
				b.context(ci)
			}
		}
	}
}

func isWrapperField(fd protoreflect.FieldDescriptor) bool {
	if fd.Message() == nil {
		return false
	}
	n := fd.Message().FullName()
	return n == futureProofName || n == deviceSentName
}

var contextInfoName = (&waE2E.ContextInfo{}).ProtoReflect().Descriptor().FullName()

// contextInfo finds the ContextInfo field of any content message.
func contextInfo(m protoreflect.Message) (*waE2E.ContextInfo, bool) {
	fd := m.Descriptor().Fields().ByName("contextInfo")
	if fd == nil || fd.Message() == nil || fd.Message().FullName() != contextInfoName || !m.Has(fd) {
		return nil, false
	}
	ci, ok := m.Get(fd).Message().Interface().(*waE2E.ContextInfo)
	return ci, ok
}
