package envelope

import "github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"

// ManyTimesThreshold is the ForwardingScore at which WhatsApp shows "forwarded many times".
const ManyTimesThreshold = 5

// StatusBroadcast is the chat that statuses are posted to. A reply quoting a message from it
// is a reply to a status (DESIGN.md §6.18).
const StatusBroadcast wa.JID = "status@broadcast"

// Kind is the content type of a message. The values are stable snake_case names: they end up
// in golden files, metrics and evidence, so never rename one.
type Kind string

// Message kinds. View-once has no kind of its own: it's the Envelope.IsViewOnce flag on the
// kind of the content inside.
const (
	KindUnknown       Kind = "unknown" // a type the builder doesn't recognize (DESIGN.md §6.13)
	KindText          Kind = "text"
	KindImage         Kind = "image"
	KindVideo         Kind = "video"
	KindPTV           Kind = "ptv" // round video note
	KindSticker       Kind = "sticker"
	KindStickerPack   Kind = "sticker_pack"
	KindDocument      Kind = "document"
	KindAudio         Kind = "audio"
	KindVoice         Kind = "voice" // push-to-talk audio
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
	KindInteractive   Kind = "interactive" // buttons, lists, templates
	KindUndecryptable Kind = "undecryptable"
)

// WrapperTruncated ends Envelope.Wrappers when the builder stopped at a cap: the message nests
// wrappers deeper, or carries more contact cards, than any real client sends, and content past
// the cap is missing.
const WrapperTruncated = "(truncated)"

// Text surface sources (TextSurface.Source). Builders may use more, such as "doc_file_name".
const (
	SourceBody         = "body"
	SourceCaption      = "caption"
	SourceLinkTitle    = "link_title"
	SourceVCardBizDesc = "vcard_biz_desc"
	SourcePollOption   = "poll_option"
)

// Envelope is the normalized form of one message (DESIGN.md §5.3). Analyzers read envelopes,
// never raw protobufs. Every field is exported and JSON-encodable, so envelopes can be stored
// as golden files.
type Envelope struct {
	Bot, Group      wa.JID
	Sender          Identity
	MsgID           wa.MessageID // the original message's ID; an edit points here
	Kind            Kind
	IsEdit          bool
	IsViewOnce      bool
	IsForwarded     bool
	ForwardingScore int // ContextInfo.ForwardingScore

	Texts    []TextSurface
	URLs     []URLRef     // extracted, preview, vCard, location and event-join URLs
	Media    []MediaRef   // attachments and thumbnails
	Contacts []ContactRef // parsed vCards
	Channels []ChannelRef // newsletters forwarded from, shared or invited to
	Invites  []InviteRef  // group invites
	Mentions []wa.JID     // mentioned people and groups
	Ads      *AdRef       // click-to-WhatsApp ad context
	Quoted   *QuotedRef   // the message this one replies to
	Status   *StatusRef   // status context, for status shares and group status posts
	Wrappers []string     // wrappers the unwrapper removed, outermost first; see WrapperTruncated

	// Phones are the numbers the message advertises, in E.164: written in its text surfaces or
	// carried by its contact cards. The normalizer (DESIGN.md §7.2 step 7) fills them in.
	Phones []PhoneRef
}

// PhoneRef is a phone number found in a message. Source is the text surface source it was
// written in ("body", "caption", …) or "vcard".
type PhoneRef struct {
	E164   string
	Source string
}

// ForwardedManyTimes reports whether WhatsApp marks the message "forwarded many times"
// (ForwardingScore >= 5).
func (e Envelope) ForwardedManyTimes() bool { return e.ForwardingScore >= ManyTimesThreshold }

// Identity is a snapshot of the sender as seen on the message: the LID is the primary
// identity, the phone-number JID is set only when known (DESIGN.md §7.7).
type Identity struct {
	LID, PN      wa.JID
	PushName     string
	VerifiedName string
}

// TextSurface is one piece of text in a message. Raw is the text exactly as received and is
// what evidence shows; Norm is empty until the normalizer (DESIGN.md §7.2) fills it.
type TextSurface struct {
	Source string
	Raw    string
	Norm   string

	// The other normalized variants (DESIGN.md §7.2), also filled by the normalizer: the
	// confusables skeleton, the leetspeak fold and the Arabic match form.
	Skeleton string
	Leet     string
	Loose    string
	// Stripped counts the invisible and combining-flood characters removed from Raw.
	Stripped int
	// EmojiTokens are the spam-emoji tokens found in Raw, in order.
	EmojiTokens []string
}

// URLRef is a URL found in a message. Source says where: "text", "preview", "vcard",
// "location", "event" and so on.
type URLRef struct {
	URL    string
	Source string
}

// MediaRef is a media attachment, or the thumbnail of one. Downloadable is false when the
// message carries only a thumbnail or no usable media key.
type MediaRef struct {
	Type         Kind
	SHA256       []byte
	Mimetype     string
	Size         int64
	Thumb        []byte
	Downloadable bool

	Width, Height int      // pixels, for images, videos and stickers
	Seconds       int      // duration of audio and video
	PageCount     int      // documents
	Flags         []string // Flag* values, such as "animated" or "ai" for stickers
}

// Media flags (MediaRef.Flags).
const (
	FlagAnimated = "animated"
	FlagLottie   = "lottie"
	FlagAvatar   = "avatar"
	FlagAI       = "ai"
	FlagGIF      = "gif"
)

// ContactRef is a parsed vCard: a contact card or one card of a contacts array.
type ContactRef struct {
	DisplayName    string
	FullName       string   // FN
	Org            string   // ORG
	Phones         []string // TEL values as written
	WAIDs          []string // TEL waid= numbers
	BizName        string   // X-WA-BIZ-NAME
	BizDescription string   // X-WA-BIZ-DESCRIPTION
	URLs           []string // URL
	IsSelfContact  bool     // the card shares the sender's own number
}

// ChannelRef is a channel (newsletter) reference. Source says where it came from:
// "forwarded", "invite", "link", "status_reshare" and so on.
type ChannelRef struct {
	JID        wa.JID
	Name       string
	InviteCode string
	Source     string
}

// InviteRef is a group invite: an invite message (Group is set) or a chat.whatsapp.com link
// (only Code is set).
type InviteRef struct {
	Code      string
	Group     wa.JID
	GroupName string
	Source    string
}

// AdRef is the click-to-WhatsApp ad context (ContextInfo.ExternalAdReply).
type AdRef struct {
	Title     string
	Body      string
	SourceURL string
}

// QuotedRef is the message a reply quotes.
type QuotedRef struct {
	ID        wa.MessageID
	Sender    wa.JID
	RemoteJID wa.JID // the quoted message's chat; empty when it's the same chat
	Kind      Kind
}

// IsStatus reports whether the reply quotes a status. Broadcast lists, groups and an empty
// RemoteJID are not statuses.
func (q QuotedRef) IsStatus() bool { return q.RemoteJID == StatusBroadcast }

// StatusRef is the status context of a message (DESIGN.md §6.18). The type fields carry the
// protobuf enum names, such as "IMAGE" or "RESHARED_FROM_POST_MANY_TIMES".
type StatusRef struct {
	ForwardedFromStatus bool // ContextInfo.ForwardOrigin is STATUS
	SourceType          string
	AttributionType     string
	Attributions        []StatusAttribution
	IsGroupStatus       bool // a Group Status post
	IsStatusMention     bool // a status that mentions the group
}

// StatusAttribution is one status attribution: where reshared status content came from.
type StatusAttribution struct {
	Type              string
	ActionURL         string
	ExternalShareURLs []string
	ChannelJID        wa.JID // a reshared channel post
	AuthorJID         wa.JID // the author of a Group Status post
}
