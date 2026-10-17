package adapter

import (
	"go.mau.fi/whatsmeow/proto/waE2E"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// context reads a ContextInfo: forwarding, mentions, the quoted message, ad reply, channel
// forward and status context (DESIGN.md §5.3, §6.18).
func (b *builder) context(ci *waE2E.ContextInfo) {
	e := b.env
	if ci.GetIsForwarded() {
		e.IsForwarded = true
	}
	if s := int(ci.GetForwardingScore()); s > e.ForwardingScore {
		e.ForwardingScore = s
		e.IsForwarded = true
	}
	for _, j := range ci.GetMentionedJID() {
		b.mention(parseJID(j))
	}
	for _, gm := range ci.GetGroupMentions() {
		b.mention(parseJID(gm.GetGroupJID()))
	}
	if ad := ci.GetExternalAdReply(); ad != nil && e.Ads == nil {
		e.Ads = &envelope.AdRef{Title: ad.GetTitle(), Body: ad.GetBody(), SourceURL: ad.GetSourceURL()}
		b.text("ad_title", ad.GetTitle())
		b.text("ad_body", ad.GetBody())
		b.text("ad_greeting", ad.GetGreetingMessageBody())
		b.url("ad", ad.GetSourceURL())
		b.url("ad", ad.GetWtwaWebsiteURL())
		b.thumb(envelope.KindImage, ad.GetThumbnail())
	}
	if nl := ci.GetForwardedNewsletterMessageInfo(); nl != nil {
		b.channel(envelope.ChannelRef{JID: parseJID(nl.GetNewsletterJID()), Name: nl.GetNewsletterName(), Source: "forwarded"})
		b.text("channel_name", nl.GetNewsletterName())
	}
	b.statusContext(ci)
	if ci.GetStanzaID() != "" && e.Quoted == nil {
		b.quoted(ci)
	}
}

func (b *builder) mention(j wa.JID) {
	if j == "" {
		return
	}
	for _, have := range b.env.Mentions {
		if have == j {
			return
		}
	}
	b.env.Mentions = append(b.env.Mentions, j)
}

func (b *builder) channel(c envelope.ChannelRef) {
	if c.JID == "" && c.InviteCode == "" {
		return
	}
	for _, have := range b.env.Channels {
		if have.JID == c.JID && have.InviteCode == c.InviteCode && have.Source == c.Source {
			return
		}
	}
	b.env.Channels = append(b.env.Channels, c)
}

// statusContext fills StatusRef from a status shared or reshared into the chat.
func (b *builder) statusContext(ci *waE2E.ContextInfo) {
	fromStatus := ci.GetForwardOrigin() == waE2E.ContextInfo_STATUS
	if !fromStatus && ci.StatusSourceType == nil && ci.StatusAttributionType == nil &&
		len(ci.GetStatusAttributions()) == 0 && !ci.GetIsGroupStatus() {
		return
	}
	s := b.status()
	s.ForwardedFromStatus = s.ForwardedFromStatus || fromStatus
	s.IsGroupStatus = s.IsGroupStatus || ci.GetIsGroupStatus()
	if ci.StatusSourceType != nil {
		s.SourceType = ci.GetStatusSourceType().String()
	}
	if ci.StatusAttributionType != nil {
		s.AttributionType = ci.GetStatusAttributionType().String()
	}
	for _, a := range ci.GetStatusAttributions() {
		attr := envelope.StatusAttribution{Type: a.GetType().String(), ActionURL: a.GetActionURL()}
		b.url("status_attribution", a.GetActionURL())
		if ext := a.GetExternalShare(); ext != nil {
			for _, u := range []string{ext.GetActionURL(), ext.GetActionFallbackURL()} {
				if u != "" {
					attr.ExternalShareURLs = append(attr.ExternalShareURLs, u)
					b.url("status_attribution", u)
				}
			}
		}
		if md := a.GetStatusReshare().GetMetadata(); md != nil && md.GetChannelJID() != "" {
			attr.ChannelJID = parseJID(md.GetChannelJID())
			b.channel(envelope.ChannelRef{JID: attr.ChannelJID, Source: "status_reshare"})
		}
		if gs := a.GetGroupStatus(); gs != nil {
			attr.AuthorJID = parseJID(gs.GetAuthorJID())
		}
		s.Attributions = append(s.Attributions, attr)
	}
}

// quoted records the reply target. A quoted status is shown to the whole group with the reply,
// so its content is analyzed too, with "quoted_status_" sources (DESIGN.md §6.18).
func (b *builder) quoted(ci *waE2E.ContextInfo) {
	q := &envelope.QuotedRef{
		ID:        wa.MessageID(ci.GetStanzaID()),
		Sender:    parseJID(ci.GetParticipant()),
		RemoteJID: parseJID(ci.GetRemoteJID()),
	}
	b.env.Quoted = q
	if ci.GetQuotedMessage() == nil || b.nested >= maxNested {
		return
	}
	sub := newBuilder(b.nested + 1)
	u := Unwrap(ci.GetQuotedMessage())
	sub.wrappers(u)
	for _, l := range u.Layers {
		sub.layer(l)
	}
	q.Kind = sub.env.Kind
	if q.Kind == "" {
		q.Kind = envelope.KindUnknown
	}
	if q.IsStatus() {
		b.merge(sub, "quoted_status_")
	}
}

// merge copies a nested build's content surfaces into b, prefixing their sources.
func (b *builder) merge(sub *builder, prefix string) {
	for _, t := range sub.env.Texts {
		b.text(prefix+t.Source, t.Raw)
	}
	for _, u := range sub.env.URLs {
		b.url(prefix+u.Source, u.URL)
	}
	b.env.Media = append(b.env.Media, sub.env.Media...)
	b.env.Contacts = append(b.env.Contacts, sub.env.Contacts...)
	for _, c := range sub.env.Channels {
		c.Source = prefix + c.Source
		b.channel(c)
	}
	for _, in := range sub.env.Invites {
		in.Source = prefix + in.Source
		b.env.Invites = append(b.env.Invites, in)
	}
	b.unknown = append(b.unknown, sub.unknown...)
}

// nestedMessage builds a message embedded in another (a payment note, a comment) and merges
// its surfaces under prefix.
func (b *builder) nestedMessage(msg *waE2E.Message, prefix string) {
	if msg == nil || b.nested >= maxNested {
		return
	}
	sub := newBuilder(b.nested + 1)
	for _, l := range Unwrap(msg).Layers {
		sub.layer(l)
	}
	b.merge(sub, prefix)
}
