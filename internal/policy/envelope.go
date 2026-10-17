package policy

import (
	"net/url"
	"strings"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
)

// sourceDocFileName is the text surface holding a document's file name.
const sourceDocFileName = "doc_file_name"

// TypeOf picks the auto-ban matrix row for a message (DESIGN.md §9.2). The most specific
// spam vector wins: view-once, status, invites, channel shares and contacts before the media
// kind, and a text with a link counts as links.
func TypeOf(e envelope.Envelope) ContentType {
	switch {
	case e.IsViewOnce:
		return TypeViewOnce
	case isStatus(e):
		return TypeStatus
	case e.Kind == envelope.KindGroupInvite || len(e.Invites) > 0:
		return TypeGroupInvites
	case e.Kind == envelope.KindChannelInvite || len(e.Channels) > 0:
		return TypeChannelShares
	case e.Kind == envelope.KindContact || len(e.Contacts) > 0:
		return TypeContacts
	}
	switch e.Kind {
	case envelope.KindDocument:
		return TypeDocuments
	case envelope.KindVideo, envelope.KindPTV:
		return TypeVideos
	case envelope.KindImage:
		return TypeImages
	case envelope.KindSticker, envelope.KindStickerPack:
		return TypeStickers
	case envelope.KindAudio, envelope.KindVoice:
		return TypeAudio
	case envelope.KindPoll:
		return TypePolls
	case envelope.KindLocation, envelope.KindLiveLocation, envelope.KindEvent:
		return TypeLocationsEvents
	}
	if len(e.URLs) > 0 {
		return TypeLinks
	}
	return TypeText
}

func isStatus(e envelope.Envelope) bool {
	return e.Status != nil && (e.Status.ForwardedFromStatus || e.Status.IsGroupStatus || e.Status.IsStatusMention || len(e.Status.Attributions) > 0)
}

// FactsOf lists the content types present in a message for the content policy (§9.3).
func FactsOf(e envelope.Envelope) ContentFacts {
	f := ContentFacts{
		GroupInvites:       e.Kind == envelope.KindGroupInvite || len(e.Invites) > 0,
		ChannelShares:      e.Kind == envelope.KindChannelInvite || len(e.Channels) > 0,
		Contacts:           e.Kind == envelope.KindContact || len(e.Contacts) > 0,
		Images:             e.Kind == envelope.KindImage,
		Videos:             e.Kind == envelope.KindVideo || e.Kind == envelope.KindPTV,
		Stickers:           e.Kind == envelope.KindSticker || e.Kind == envelope.KindStickerPack,
		HasDocument:        e.Kind == envelope.KindDocument,
		Audio:              e.Kind == envelope.KindAudio || e.Kind == envelope.KindVoice,
		Polls:              e.Kind == envelope.KindPoll,
		Locations:          e.Kind == envelope.KindLocation || e.Kind == envelope.KindLiveLocation,
		Events:             e.Kind == envelope.KindEvent,
		ViewOnce:           e.IsViewOnce,
		ForwardedManyTimes: e.ForwardedManyTimes(),
	}
	if e.Status != nil {
		f.StatusShares = e.Status.ForwardedFromStatus || len(e.Status.Attributions) > 0
		f.StatusMentions = e.Status.IsStatusMention
		f.GroupStatusPosts = e.Status.IsGroupStatus
	}
	for _, u := range e.URLs {
		if h := hostOf(u.URL); h != "" {
			f.LinkHosts = append(f.LinkHosts, h)
		}
	}
	for _, t := range e.Texts {
		if t.Source == sourceDocFileName {
			f.Documents = append(f.Documents, t.Raw)
		}
	}
	for _, m := range e.Media {
		if m.Type != envelope.KindDocument {
			continue
		}
		f.HasDocument = true
		if m.Mimetype != "" {
			f.DocumentMimetypes = append(f.DocumentMimetypes, m.Mimetype)
		}
	}
	return f
}

// hostOf returns a URL's host (with port, which normHost strips), accepting URLs written
// without a scheme.
func hostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}
