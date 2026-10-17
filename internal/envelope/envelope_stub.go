package envelope

import "github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"

// Stub declarations for F-024 (see .agent/contracts/F-024.md). The builder replaces this file.

// Kind is the content type of a message.
type Kind string

// Message kinds.
const (
	KindUnknown       Kind = "unknown"
	KindText          Kind = "text"
	KindImage         Kind = "image"
	KindVideo         Kind = "video"
	KindPTV           Kind = "ptv"
	KindSticker       Kind = "sticker"
	KindStickerPack   Kind = "sticker_pack"
	KindDocument      Kind = "document"
	KindAudio         Kind = "audio"
	KindVoice         Kind = "voice"
	KindContact       Kind = "contact"
	KindLocation      Kind = "location"
	KindLiveLocation  Kind = "live_location"
	KindEvent         Kind = "event"
	KindPoll          Kind = "poll"
	KindGroupInvite   Kind = "group_invite"
	KindChannelInvite Kind = "channel_invite"
	KindProduct       Kind = "product"
	KindOrder         Kind = "order"
	KindPayment       Kind = "payment"
	KindInteractive   Kind = "interactive"
	KindUndecryptable Kind = "undecryptable"
)

// Text surface sources.
const (
	SourceBody         = "body"
	SourceCaption      = "caption"
	SourceLinkTitle    = "link_title"
	SourceVCardBizDesc = "vcard_biz_desc"
	SourcePollOption   = "poll_option"
)

// Envelope is the normalized form of one message.
type Envelope struct {
	Bot, Group      wa.JID
	Sender          Identity
	MsgID           wa.MessageID
	Kind            Kind
	IsEdit          bool
	IsViewOnce      bool
	IsForwarded     bool
	ForwardingScore int

	Texts    []TextSurface
	URLs     []URLRef
	Media    []MediaRef
	Contacts []ContactRef
	Channels []ChannelRef
	Invites  []InviteRef
	Mentions []wa.JID
	Ads      *AdRef
	Quoted   *QuotedRef
	Status   *StatusRef
	Wrappers []string
}

// ForwardedManyTimes reports whether the message was forwarded many times.
func (e Envelope) ForwardedManyTimes() bool { panic("not implemented") }

// Identity is the sender as seen on the message.
type Identity struct {
	LID, PN      wa.JID
	PushName     string
	VerifiedName string
}

// TextSurface is one piece of text in a message.
type TextSurface struct {
	Source string
	Raw    string
	Norm   string
}

// URLRef is a URL found in a message.
type URLRef struct {
	URL    string
	Source string
}

// MediaRef is a media attachment or thumbnail.
type MediaRef struct {
	Type         Kind
	SHA256       []byte
	Mimetype     string
	Size         int64
	Thumb        []byte
	Downloadable bool
}

// ContactRef is a parsed vCard.
type ContactRef struct {
	DisplayName    string
	FullName       string
	Org            string
	Phones         []string
	WAIDs          []string
	BizName        string
	BizDescription string
	URLs           []string
	IsSelfContact  bool
}

// ChannelRef is a channel (newsletter) reference.
type ChannelRef struct {
	JID        wa.JID
	Name       string
	InviteCode string
	Source     string
}

// InviteRef is a group invite reference.
type InviteRef struct {
	Code      string
	Group     wa.JID
	GroupName string
	Source    string
}

// AdRef is a click-to-WhatsApp ad reply context.
type AdRef struct {
	Title     string
	Body      string
	SourceURL string
}

// QuotedRef is the message a reply quotes.
type QuotedRef struct {
	ID        wa.MessageID
	Sender    wa.JID
	RemoteJID wa.JID
	Kind      Kind
}

// IsStatus reports whether the quoted message is a status.
func (q QuotedRef) IsStatus() bool { panic("not implemented") }

// StatusRef is the status context of a message.
type StatusRef struct {
	ForwardedFromStatus bool
	SourceType          string
	AttributionType     string
	Attributions        []StatusAttribution
	IsGroupStatus       bool
	IsStatusMention     bool
}

// StatusAttribution is one status attribution.
type StatusAttribution struct {
	Type              string
	ActionURL         string
	ExternalShareURLs []string
	ChannelJID        wa.JID
	AuthorJID         wa.JID
}
