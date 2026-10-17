package adapter

import (
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
)

// handler reads one waE2E.Message field into the envelope.
type handler func(b *builder, m *waE2E.Message)

// handlers (with generics) covers every content field of waE2E.Message, keyed by proto field name. Fields that
// are neither here nor in ignored (nor wrappers, which Unwrap removes) are unknown: their
// strings are read by reflection. TestEveryMessageFieldClassified fails when a whatsmeow
// update adds a field, so new types get a decision.
var handlers map[string]handler

// The handlers refer back to the builder (nested messages), so they're set in init.
func init() { handlers = handlerTable() }

func handlerTable() map[string]handler {
	return map[string]handler{
		"conversation": func(b *builder, m *waE2E.Message) {
			b.kind(envelope.KindText)
			b.text(envelope.SourceBody, m.GetConversation())
		},
		"extendedTextMessage": func(b *builder, m *waE2E.Message) { b.extendedText(m.GetExtendedTextMessage()) },

		"imageMessage":        func(b *builder, m *waE2E.Message) { b.image(m.GetImageMessage()) },
		"instantImageMessage": func(b *builder, m *waE2E.Message) { b.image(m.GetInstantImageMessage()) },
		"videoMessage":        func(b *builder, m *waE2E.Message) { b.video(m.GetVideoMessage(), envelope.KindVideo) },
		"ptvMessage":          func(b *builder, m *waE2E.Message) { b.video(m.GetPtvMessage(), envelope.KindPTV) },
		"audioMessage":        func(b *builder, m *waE2E.Message) { b.audio(m.GetAudioMessage()) },
		"documentMessage":     func(b *builder, m *waE2E.Message) { b.document(m.GetDocumentMessage()) },
		"stickerMessage":      func(b *builder, m *waE2E.Message) { b.sticker(m.GetStickerMessage()) },
		"stickerPackMessage":  func(b *builder, m *waE2E.Message) { b.stickerPack(m.GetStickerPackMessage()) },

		"contactMessage": func(b *builder, m *waE2E.Message) {
			b.kind(envelope.KindContact)
			b.contact(m.GetContactMessage())
		},
		"contactsArrayMessage": func(b *builder, m *waE2E.Message) {
			b.kind(envelope.KindContact)
			b.text("contacts_display_name", m.GetContactsArrayMessage().GetDisplayName())
			for _, c := range m.GetContactsArrayMessage().GetContacts() {
				b.contact(c)
			}
		},

		"groupInviteMessage":                func(b *builder, m *waE2E.Message) { b.groupInvite(m.GetGroupInviteMessage()) },
		"newsletterFollowerInviteMessageV2": func(b *builder, m *waE2E.Message) { b.channelInvite(m.GetNewsletterFollowerInviteMessageV2()) },
		"newsletterAdminInviteMessage": func(b *builder, m *waE2E.Message) {
			in := m.GetNewsletterAdminInviteMessage()
			b.channelInvite(&waE2E.NewsletterFollowerInviteMessage{
				NewsletterJID: in.NewsletterJID, NewsletterName: in.NewsletterName,
				JPEGThumbnail: in.JPEGThumbnail, Caption: in.Caption,
			})
		},

		"pollCreationMessage":         func(b *builder, m *waE2E.Message) { b.poll(m.GetPollCreationMessage()) },
		"pollCreationMessageV2":       func(b *builder, m *waE2E.Message) { b.poll(m.GetPollCreationMessageV2()) },
		"pollCreationMessageV3":       func(b *builder, m *waE2E.Message) { b.poll(m.GetPollCreationMessageV3()) },
		"pollCreationMessageV5":       func(b *builder, m *waE2E.Message) { b.poll(m.GetPollCreationMessageV5()) },
		"pollCreationMessageV6":       func(b *builder, m *waE2E.Message) { b.poll(m.GetPollCreationMessageV6()) },
		"pollResultSnapshotMessage":   func(b *builder, m *waE2E.Message) { b.pollResult(m.GetPollResultSnapshotMessage()) },
		"pollResultSnapshotMessageV3": func(b *builder, m *waE2E.Message) { b.pollResult(m.GetPollResultSnapshotMessageV3()) },
		"pollAddOptionMessage": func(b *builder, m *waE2E.Message) {
			b.kind(envelope.KindPoll)
			b.text(envelope.SourcePollOption, m.GetPollAddOptionMessage().GetAddOption().GetOptionName())
		},

		"locationMessage": func(b *builder, m *waE2E.Message) { b.location(m.GetLocationMessage(), "location") },
		"liveLocationMessage": func(b *builder, m *waE2E.Message) {
			b.kind(envelope.KindLiveLocation)
			b.text(envelope.SourceCaption, m.GetLiveLocationMessage().GetCaption())
		},
		"eventMessage": func(b *builder, m *waE2E.Message) { b.event(m.GetEventMessage()) },
		"eventInviteMessage": func(b *builder, m *waE2E.Message) {
			ev := m.GetEventInviteMessage()
			b.kind(envelope.KindEvent)
			b.text("event_name", ev.GetEventTitle())
			b.text(envelope.SourceCaption, ev.GetCaption())
			b.text("event_location", ev.GetLocationName())
			b.url("event", ev.GetCallLink())
			b.thumb(envelope.KindImage, ev.GetJPEGThumbnail())
		},
		"scheduledCallCreationMessage": func(b *builder, m *waE2E.Message) {
			b.kind(envelope.KindEvent)
			b.text("event_name", m.GetScheduledCallCreationMessage().GetTitle())
		},

		"productMessage": func(b *builder, m *waE2E.Message) { b.product(m.GetProductMessage()) },
		"orderMessage": func(b *builder, m *waE2E.Message) {
			o := m.GetOrderMessage()
			b.kind(envelope.KindOrder)
			b.text("order_title", o.GetOrderTitle())
			b.text("order_message", o.GetMessage())
			b.thumb(envelope.KindImage, o.GetThumbnail())
		},
		"requestPaymentMessage": func(b *builder, m *waE2E.Message) {
			b.kind(envelope.KindPayment)
			b.nestedMessage(m.GetRequestPaymentMessage().GetNoteMessage(), "payment_note_")
		},
		"sendPaymentMessage": func(b *builder, m *waE2E.Message) {
			b.kind(envelope.KindPayment)
			b.nestedMessage(m.GetSendPaymentMessage().GetNoteMessage(), "payment_note_")
		},
		"invoiceMessage": func(b *builder, m *waE2E.Message) {
			b.kind(envelope.KindPayment)
			b.text("payment_note", m.GetInvoiceMessage().GetNote())
			b.thumb(envelope.KindImage, m.GetInvoiceMessage().GetAttachmentJPEGThumbnail())
		},

		"statusQuotedMessage": func(b *builder, m *waE2E.Message) {
			sq := m.GetStatusQuotedMessage()
			b.kind(envelope.KindText)
			b.text("quoted_status_body", sq.GetText())
			b.thumb(envelope.KindImage, sq.GetThumbnail())
			b.status()
		},
		"commentMessage": func(b *builder, m *waE2E.Message) {
			b.kind(envelope.KindText)
			b.nestedMessage(m.GetCommentMessage().GetMessage(), "comment_")
		},
	}
}

// ignored lists the waE2E.Message fields with nothing to analyze: key distribution and other
// protocol traffic, encrypted payloads, reactions and votes (handled by other parts of the
// bot), and metadata-only messages. A ProtocolMessage is read by Unwrap (edits) and Message
// (revokes); its other types carry no content.
var ignored = map[string]struct{}{
	"senderKeyDistributionMessage":               {},
	"fastRatchetKeySenderKeyDistributionMessage": {},
	"messageContextInfo":                         {},
	"protocolMessage":                            {},
	"call":                                       {},
	"callLogMesssage":                            {},
	"bcallMessage":                               {},
	"reactionMessage":                            {},
	"encReactionMessage":                         {},
	"pollUpdateMessage":                          {},
	"keepInChatMessage":                          {},
	"pinInChatMessage":                           {},
	"stickerSyncRmrMessage":                      {},
	"encCommentMessage":                          {},
	"encEventResponseMessage":                    {},
	"secretEncryptedMessage":                     {},
	"conditionalRevealMessage":                   {},
	"placeholderMessage":                         {},
	"messageHistoryBundle":                       {},
	"messageHistoryNotice":                       {},
	"groupRootKeyShare":                          {},
	"rootSecretDistributeMessage":                {},
	"statusNotificationMessage":                  {},
	"statusLinkPreviewMetadata":                  {},
	"statusStickerInteractionMessage":            {},
	"albumMessage":                               {}, // counts only; the album's items arrive as their own messages
	"cancelPaymentRequestMessage":                {},
	"declinePaymentRequestMessage":               {},
	"requestLocationUpdateMessage":               {},
}

// genericSpec is a content type read by reflection: every string of the field goes to the
// text and URL pipelines under source. Used for types whose fields are many and change often,
// such as buttons, lists, templates and their replies (DESIGN.md §6.12).
type genericSpec struct {
	kind   envelope.Kind
	source string
}

var generics = map[string]genericSpec{
	"scheduledCallEditMessage":    {envelope.KindEvent, "event"},
	"paymentInviteMessage":        {envelope.KindPayment, "payment"},
	"paymentReminderMessage":      {envelope.KindPayment, "payment"},
	"splitPaymentMessage":         {envelope.KindPayment, "payment"},
	"splitPaymentUpdateMessage":   {envelope.KindPayment, "payment"},
	"buttonsMessage":              {envelope.KindInteractive, "interactive"},
	"buttonsResponseMessage":      {envelope.KindInteractive, "interactive"},
	"listMessage":                 {envelope.KindInteractive, "interactive"},
	"listResponseMessage":         {envelope.KindInteractive, "interactive"},
	"templateMessage":             {envelope.KindInteractive, "interactive"},
	"templateButtonReplyMessage":  {envelope.KindInteractive, "interactive"},
	"interactiveMessage":          {envelope.KindInteractive, "interactive"},
	"interactiveResponseMessage":  {envelope.KindInteractive, "interactive"},
	"highlyStructuredMessage":     {envelope.KindInteractive, "interactive"},
	"requestPhoneNumberMessage":   {envelope.KindInteractive, "interactive"},
	"requestLocationMessage":      {envelope.KindInteractive, "interactive"},
	"statusQuestionAnswerMessage": {envelope.KindText, "status_answer"},
	"questionResponseMessage":     {envelope.KindText, "question_response"},
	"richResponseMessage":         {envelope.KindText, "bot_response"},
	"chat":                        {envelope.KindText, "chat"},
	"musicMessage":                {envelope.KindAudio, "music"},
}

func (b *builder) extendedText(t *waE2E.ExtendedTextMessage) {
	b.kind(envelope.KindText)
	b.text(envelope.SourceBody, t.GetText())
	b.text(envelope.SourceLinkTitle, t.GetTitle())
	b.text("link_description", t.GetDescription())
	b.url("preview", t.GetMatchedText())
	b.thumb(envelope.KindImage, t.GetJPEGThumbnail())
	b.text("invite_parent_group", t.GetInviteLinkParentGroupSubjectV2())
}

type mediaFields interface {
	GetFileSHA256() []byte
	GetMimetype() string
	GetFileLength() uint64
	GetMediaKey() []byte
	GetDirectPath() string
	GetURL() string
}

func (b *builder) media(kind envelope.Kind, m mediaFields, thumb []byte) *envelope.MediaRef {
	b.env.Media = append(b.env.Media, envelope.MediaRef{
		Type:         kind,
		SHA256:       m.GetFileSHA256(),
		Mimetype:     m.GetMimetype(),
		Size:         int64(m.GetFileLength()),
		Thumb:        thumb,
		Downloadable: len(m.GetMediaKey()) > 0 && (m.GetDirectPath() != "" || m.GetURL() != ""),
	})
	return &b.env.Media[len(b.env.Media)-1]
}

// flag is a media flag and whether it's set.
type flag struct {
	name string
	on   bool
}

func flags(fs ...flag) []string {
	var out []string
	for _, f := range fs {
		if f.on {
			out = append(out, f.name)
		}
	}
	return out
}

func (b *builder) image(im *waE2E.ImageMessage) {
	b.kind(envelope.KindImage)
	ref := b.media(envelope.KindImage, im, im.GetJPEGThumbnail())
	ref.Width, ref.Height = int(im.GetWidth()), int(im.GetHeight())
	b.text(envelope.SourceCaption, im.GetCaption())
	b.text("accessibility_label", im.GetAccessibilityLabel())
	b.url("qr", im.GetQrURL())
	b.env.IsViewOnce = b.env.IsViewOnce || im.GetViewOnce()
}

func (b *builder) video(v *waE2E.VideoMessage, kind envelope.Kind) {
	b.kind(kind)
	ref := b.media(kind, v, v.GetJPEGThumbnail())
	ref.Width, ref.Height, ref.Seconds = int(v.GetWidth()), int(v.GetHeight()), int(v.GetSeconds())
	ref.Flags = flags(flag{envelope.FlagGIF, v.GetGifPlayback()})
	b.text(envelope.SourceCaption, v.GetCaption())
	b.text("accessibility_label", v.GetAccessibilityLabel())
	b.env.IsViewOnce = b.env.IsViewOnce || v.GetViewOnce()
}

func (b *builder) audio(a *waE2E.AudioMessage) {
	kind := envelope.KindAudio
	if a.GetPTT() {
		kind = envelope.KindVoice
	}
	b.kind(kind)
	b.media(kind, a, nil).Seconds = int(a.GetSeconds())
	b.text("accessibility_label", a.GetAccessibilityLabel())
	b.env.IsViewOnce = b.env.IsViewOnce || a.GetViewOnce()
}

func (b *builder) document(d *waE2E.DocumentMessage) {
	b.kind(envelope.KindDocument)
	b.media(envelope.KindDocument, d, d.GetJPEGThumbnail()).PageCount = int(d.GetPageCount())
	b.text(envelope.SourceCaption, d.GetCaption())
	b.text("doc_file_name", d.GetFileName())
	if d.GetTitle() != d.GetFileName() {
		b.text("doc_title", d.GetTitle())
	}
	b.text("accessibility_label", d.GetAccessibilityLabel())
}

func (b *builder) sticker(s *waE2E.StickerMessage) {
	b.kind(envelope.KindSticker)
	ref := b.media(envelope.KindSticker, s, s.GetPngThumbnail())
	ref.Width, ref.Height = int(s.GetWidth()), int(s.GetHeight())
	ref.Flags = flags(flag{envelope.FlagAnimated, s.GetIsAnimated()}, flag{envelope.FlagLottie, s.GetIsLottie()},
		flag{envelope.FlagAvatar, s.GetIsAvatar()}, flag{envelope.FlagAI, s.GetIsAiSticker()})
	b.text("sticker_label", s.GetAccessibilityLabel())
	b.text("sticker_emojis", s.GetEmojis())
}

func (b *builder) stickerPack(p *waE2E.StickerPackMessage) {
	b.kind(envelope.KindStickerPack)
	b.env.Media = append(b.env.Media, envelope.MediaRef{
		Type:         envelope.KindStickerPack,
		SHA256:       p.GetFileSHA256(),
		Size:         int64(p.GetFileLength()),
		Downloadable: len(p.GetMediaKey()) > 0 && p.GetDirectPath() != "",
	})
	b.text("sticker_pack_name", p.GetName())
	b.text("sticker_pack_publisher", p.GetPublisher())
	b.text("sticker_pack_description", p.GetPackDescription())
	b.text(envelope.SourceCaption, p.GetCaption())
	for _, s := range p.GetStickers() {
		b.text("sticker_label", s.GetAccessibilityLabel())
		b.text("sticker_emojis", strings.Join(s.GetEmojis(), " "))
	}
}

// maxContacts bounds the cards read from one message, so a contacts array can't multiply the
// vCard parser's cost.
const maxContacts = 32

// contact parses a contact card. Every vCard field is a text surface (spam hides in all of
// them), waid numbers feed number reputation, and URL lines go to the URL analyzer (§6.7).
func (b *builder) contact(c *waE2E.ContactMessage) {
	if len(b.env.Contacts) >= maxContacts {
		b.truncated = true
		b.text("contact_display_name", c.GetDisplayName())
		return
	}
	card := ParseVCard(c.GetVcard())
	phones, waids := card.Phones()
	ref := envelope.ContactRef{
		DisplayName:    c.GetDisplayName(),
		FullName:       card.First("FN"),
		Org:            card.First("ORG"),
		Phones:         phones,
		WAIDs:          waids,
		BizName:        card.First("X-WA-BIZ-NAME"),
		BizDescription: card.First("X-WA-BIZ-DESCRIPTION"),
		URLs:           card.Get("URL"),
		IsSelfContact:  c.GetIsSelfContact(),
	}
	b.env.Contacts = append(b.env.Contacts, ref)
	b.text("contact_display_name", c.GetDisplayName())
	for _, p := range card.Properties {
		switch p.Name {
		case "TEL", "PHOTO", "LOGO", "SOUND", "KEY", "UID", "REV", "PRODID", "X-WA-BIZ-AUTOMATED-TYPE":
			continue
		case "URL":
			b.url("vcard", p.Value)
		case "X-WA-BIZ-DESCRIPTION":
			b.text(envelope.SourceVCardBizDesc, p.Value)
		case "X-WA-BIZ-NAME":
			b.text("vcard_biz_name", p.Value)
		default:
			b.text("vcard_"+strings.ToLower(p.Name), p.Value)
		}
	}
}

// selfContacts marks contact cards that share the sender's own number.
func (b *builder) selfContacts() {
	own := b.env.Sender.PN.User()
	if own == "" {
		return
	}
	for i, c := range b.env.Contacts {
		for _, w := range c.WAIDs {
			if w == own {
				b.env.Contacts[i].IsSelfContact = true
			}
		}
	}
}

func (b *builder) groupInvite(g *waE2E.GroupInviteMessage) {
	b.kind(envelope.KindGroupInvite)
	b.env.Invites = append(b.env.Invites, envelope.InviteRef{
		Code:      g.GetInviteCode(),
		Group:     parseJID(g.GetGroupJID()),
		GroupName: g.GetGroupName(),
		Source:    "invite_message",
	})
	b.text("invite_group_name", g.GetGroupName())
	b.text(envelope.SourceCaption, g.GetCaption())
	b.thumb(envelope.KindImage, g.GetJPEGThumbnail())
}

func (b *builder) channelInvite(n *waE2E.NewsletterFollowerInviteMessage) {
	b.kind(envelope.KindChannelInvite)
	b.channel(envelope.ChannelRef{JID: parseJID(n.GetNewsletterJID()), Name: n.GetNewsletterName(), Source: "invite"})
	b.text("channel_name", n.GetNewsletterName())
	b.text(envelope.SourceCaption, n.GetCaption())
	b.thumb(envelope.KindImage, n.GetJPEGThumbnail())
}

func (b *builder) poll(p *waE2E.PollCreationMessage) {
	b.kind(envelope.KindPoll)
	b.text("poll_name", p.GetName())
	for _, o := range p.GetOptions() {
		b.text(envelope.SourcePollOption, o.GetOptionName())
	}
}

func (b *builder) pollResult(p *waE2E.PollResultSnapshotMessage) {
	b.kind(envelope.KindPoll)
	b.text("poll_name", p.GetName())
	for _, v := range p.GetPollVotes() {
		b.text(envelope.SourcePollOption, v.GetOptionName())
	}
}

func (b *builder) location(l *waE2E.LocationMessage, source string) {
	if l.GetIsLive() {
		b.kind(envelope.KindLiveLocation)
	}
	b.kind(envelope.KindLocation)
	b.text(source+"_name", l.GetName())
	b.text(source+"_address", l.GetAddress())
	b.text(source+"_comment", l.GetComment())
	b.url(source, l.GetURL())
}

func (b *builder) event(ev *waE2E.EventMessage) {
	b.kind(envelope.KindEvent)
	b.text("event_name", ev.GetName())
	b.text("event_description", ev.GetDescription())
	b.url("event", ev.GetJoinLink())
	if l := ev.GetLocation(); l != nil {
		b.text("event_location_name", l.GetName())
		b.text("event_location_address", l.GetAddress())
		b.text("event_location_comment", l.GetComment())
		b.url("event", l.GetURL())
	}
}

func (b *builder) product(p *waE2E.ProductMessage) {
	b.kind(envelope.KindProduct)
	s := p.GetProduct()
	b.text("product_title", s.GetTitle())
	b.text("product_description", s.GetDescription())
	b.url("product", s.GetURL())
	if im := s.GetProductImage(); im != nil {
		b.media(envelope.KindImage, im, im.GetJPEGThumbnail())
	}
	b.text(envelope.SourceBody, p.GetBody())
	b.text("product_footer", p.GetFooter())
	b.text("catalog_title", p.GetCatalog().GetTitle())
	b.text("catalog_description", p.GetCatalog().GetDescription())
}
