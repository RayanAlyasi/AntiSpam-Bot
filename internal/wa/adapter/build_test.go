package adapter

import (
	"slices"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waStatusAttributions"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const bot wa.JID = "100@s.whatsapp.net"

var (
	groupJID  = types.NewJID("1203", types.GroupServer)
	senderLID = types.NewADJID("555", 0, 3)
	senderPN  = types.NewJID("966500000001", types.DefaultUserServer)
	s         = proto.String
)

func init() { senderLID.Server = types.HiddenUserServer }

func event(msg *waE2E.Message) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: groupJID, Sender: senderLID, SenderAlt: senderPN, IsGroup: true},
			ID:            "MSG1",
			PushName:      "Promo",
			Timestamp:     time.Unix(1, 0),
		},
		Message: msg,
	}
}

func build(t *testing.T, msg *waE2E.Message) *envelope.Envelope {
	t.Helper()
	res := Build(event(msg), bot)
	if res.Envelope == nil {
		t.Fatalf("no envelope for %v", msg)
	}
	return res.Envelope
}

// texts maps each text source to its raw values.
func texts(e *envelope.Envelope) map[string][]string {
	out := map[string][]string{}
	for _, t := range e.Texts {
		out[t.Source] = append(out[t.Source], t.Raw)
	}
	return out
}

func urls(e *envelope.Envelope) map[string][]string {
	out := map[string][]string{}
	for _, u := range e.URLs {
		out[u.Source] = append(out[u.Source], u.URL)
	}
	return out
}

func wantText(t *testing.T, e *envelope.Envelope, source string, want ...string) {
	t.Helper()
	if got := texts(e)[source]; !slices.Equal(got, want) {
		t.Errorf("texts[%q] = %q, want %q (all texts: %v)", source, got, want, texts(e))
	}
}

func wantURL(t *testing.T, e *envelope.Envelope, source string, want ...string) {
	t.Helper()
	if got := urls(e)[source]; !slices.Equal(got, want) {
		t.Errorf("urls[%q] = %q, want %q (all URLs: %v)", source, got, want, urls(e))
	}
}

func TestEnvelopeHeader(t *testing.T) {
	evt := event(&waE2E.Message{Conversation: s("hi")})
	evt.Info.VerifiedName = &types.VerifiedName{}
	e := Build(evt, bot).Envelope
	want := envelope.Identity{LID: "555@lid", PN: "966500000001@s.whatsapp.net", PushName: "Promo"}
	if e.Bot != bot || e.Group != "1203@g.us" || e.MsgID != "MSG1" || e.Sender != want || e.Kind != envelope.KindText {
		t.Errorf("header = %+v, want bot, group, ID, kind text and sender %+v (keyed by LID, device part dropped)", e, want)
	}

	// A PN-addressed message carries the LID as SenderAlt.
	evt = event(&waE2E.Message{Conversation: s("hi")})
	evt.Info.Sender, evt.Info.SenderAlt = senderPN, types.NewJID("555", types.HiddenUserServer)
	if got := Build(evt, bot).Envelope.Sender; got.LID != "555@lid" || got.PN != "966500000001@s.whatsapp.net" {
		t.Errorf("PN-addressed sender = %+v, want both LID and PN", got)
	}
}

func TestTextPreviewMentionsQuoteAd(t *testing.T) {
	e := build(t, &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text:          s("Join now https://bit.ly/x @555"),
		MatchedText:   s("https://bit.ly/x"),
		Title:         s("FREE iPhone"),
		Description:   s("Claim today"),
		JPEGThumbnail: []byte{0xff, 0xd8},
		ContextInfo: &waE2E.ContextInfo{
			MentionedJID:    []string{"555@lid", "777:2@s.whatsapp.net", "555@lid"},
			GroupMentions:   []*waE2E.GroupMention{{GroupJID: s("1203@g.us"), GroupSubject: s("All")}},
			StanzaID:        s("Q1"),
			Participant:     s("888@lid"),
			QuotedMessage:   &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: s("quoted")}},
			IsForwarded:     proto.Bool(true),
			ForwardingScore: proto.Uint32(7),
			ExternalAdReply: &waE2E.ContextInfo_ExternalAdReplyInfo{Title: s("Ad title"), Body: s("Ad body"), SourceURL: s("https://fb.me/ad")},
		},
	}})
	wantText(t, e, envelope.SourceBody, "Join now https://bit.ly/x @555")
	wantText(t, e, envelope.SourceLinkTitle, "FREE iPhone")
	wantText(t, e, "link_description", "Claim today")
	wantText(t, e, "ad_title", "Ad title")
	wantText(t, e, "ad_body", "Ad body")
	wantURL(t, e, "preview", "https://bit.ly/x")
	wantURL(t, e, "ad", "https://fb.me/ad")
	if !slices.Equal(e.Mentions, []wa.JID{"555@lid", "777@s.whatsapp.net", "1203@g.us"}) {
		t.Errorf("Mentions = %v, want people and the group mention, deduplicated, no device parts", e.Mentions)
	}
	if e.Quoted == nil || *e.Quoted != (envelope.QuotedRef{ID: "Q1", Sender: "888@lid", Kind: envelope.KindImage}) {
		t.Errorf("Quoted = %+v, want Q1 from 888@lid, an image", e.Quoted)
	}
	if _, ok := texts(e)["caption"]; ok {
		t.Errorf("a normal quoted message's text must not become this message's text: %v", texts(e))
	}
	if !e.IsForwarded || e.ForwardingScore != 7 || !e.ForwardedManyTimes() {
		t.Errorf("forwarding = %v/%d, want forwarded many times", e.IsForwarded, e.ForwardingScore)
	}
	if e.Ads == nil || e.Ads.Title != "Ad title" || e.Ads.SourceURL != "https://fb.me/ad" {
		t.Errorf("Ads = %+v", e.Ads)
	}
	if len(e.Media) != 1 || e.Media[0].Downloadable || len(e.Media[0].Thumb) != 2 {
		t.Errorf("Media = %+v, want the preview thumbnail only (not downloadable)", e.Media)
	}
}

func TestMedia(t *testing.T) {
	sha := []byte{1, 2, 3}
	tests := []struct {
		name     string
		msg      *waE2E.Message
		kind     envelope.Kind
		texts    map[string][]string
		download bool
	}{
		{"image", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			FileSHA256: sha, Mimetype: s("image/jpeg"), FileLength: proto.Uint64(10), Caption: s("cap"),
			JPEGThumbnail: []byte{9}, MediaKey: []byte{1}, DirectPath: s("/p"),
		}}, envelope.KindImage, map[string][]string{"caption": {"cap"}}, true},
		{
			"video", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{FileSHA256: sha, Caption: s("vid"), AccessibilityLabel: s("alt")}},
			envelope.KindVideo,
			map[string][]string{"caption": {"vid"}, "accessibility_label": {"alt"}},
			false,
		},
		{"ptv", &waE2E.Message{PtvMessage: &waE2E.VideoMessage{FileSHA256: sha}}, envelope.KindPTV, map[string][]string{}, false},
		{"voice", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{FileSHA256: sha, PTT: proto.Bool(true)}}, envelope.KindVoice, map[string][]string{}, false},
		{"audio", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{FileSHA256: sha}}, envelope.KindAudio, map[string][]string{}, false},
		{"document", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			FileSHA256: sha, FileName: s("invoice.pdf.apk"), Title: s("Invoice"), Caption: s("open it"),
		}}, envelope.KindDocument, map[string][]string{"doc_file_name": {"invoice.pdf.apk"}, "doc_title": {"Invoice"}, "caption": {"open it"}}, false},
		{"sticker", &waE2E.Message{StickerMessage: &waE2E.StickerMessage{
			FileSHA256: sha, PngThumbnail: []byte{7}, AccessibilityLabel: s("buy crypto"), Emojis: s("💰"),
		}}, envelope.KindSticker, map[string][]string{"sticker_label": {"buy crypto"}, "sticker_emojis": {"💰"}}, false},
		{"sticker pack", &waE2E.Message{StickerPackMessage: &waE2E.StickerPackMessage{
			FileSHA256: sha, Name: s("Pack"), Publisher: s("spam.example"), PackDescription: s("desc"),
			Stickers: []*waE2E.StickerPackMessage_Sticker{{Emojis: []string{"😀", "🔥"}}},
		}}, envelope.KindStickerPack, map[string][]string{
			"sticker_pack_name": {"Pack"}, "sticker_pack_publisher": {"spam.example"},
			"sticker_pack_description": {"desc"}, "sticker_emojis": {"😀 🔥"},
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := build(t, tt.msg)
			if e.Kind != tt.kind || len(e.Media) != 1 || e.Media[0].Type != tt.kind || !slices.Equal(e.Media[0].SHA256, sha) {
				t.Fatalf("kind %s, media %+v; want one %s with the file SHA256 (the free exact fingerprint)", e.Kind, e.Media, tt.kind)
			}
			if e.Media[0].Downloadable != tt.download {
				t.Errorf("Downloadable = %v, want %v", e.Media[0].Downloadable, tt.download)
			}
			got := texts(e)
			for src, want := range tt.texts {
				if !slices.Equal(got[src], want) {
					t.Errorf("texts[%q] = %q, want %q", src, got[src], want)
				}
			}
			if len(got) != len(tt.texts) {
				t.Errorf("texts = %v, want exactly %v", got, tt.texts)
			}
		})
	}
	im := build(t, &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		FileSHA256: sha, FileLength: proto.Uint64(10), Mimetype: s("image/jpeg"), JPEGThumbnail: []byte{9},
		Width: proto.Uint32(640), Height: proto.Uint32(480),
	}})
	if m := im.Media[0]; m.Size != 10 || m.Mimetype != "image/jpeg" || len(m.Thumb) != 1 || m.Width != 640 || m.Height != 480 {
		t.Errorf("image MediaRef = %+v, want size, mimetype, dimensions and inline thumbnail", m)
	}
	st := build(t, &waE2E.Message{StickerMessage: &waE2E.StickerMessage{IsAnimated: proto.Bool(true), IsAiSticker: proto.Bool(true)}})
	if f := st.Media[0].Flags; !slices.Equal(f, []string{envelope.FlagAnimated, envelope.FlagAI}) {
		t.Errorf("sticker flags = %v, want animated and ai", f)
	}
	mixed := build(t, &waE2E.Message{Conversation: s("hi"), VideoMessage: &waE2E.VideoMessage{FileSHA256: sha}})
	if mixed.Kind != envelope.KindVideo {
		t.Errorf("text + video kind = %s, want video (adding text must not dodge a per-kind policy)", mixed.Kind)
	}
	doc := build(t, &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{PageCount: proto.Uint32(3)}})
	vid := build(t, &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Seconds: proto.Uint32(12), GifPlayback: proto.Bool(true)}})
	if doc.Media[0].PageCount != 3 || vid.Media[0].Seconds != 12 || !slices.Equal(vid.Media[0].Flags, []string{envelope.FlagGIF}) {
		t.Errorf("document pages %d, video seconds %d flags %v; want 3, 12 and gif", doc.Media[0].PageCount, vid.Media[0].Seconds, vid.Media[0].Flags)
	}
}

const bizCard = "BEGIN:VCARD\nVERSION:3.0\nFN:Easy Loans\nORG:Easy Loans LLC\nitem1.TEL;waid=966511111111:+966 51 111 1111\n" +
	"X-WA-BIZ-NAME:Easy Loans\nX-WA-BIZ-DESCRIPTION:Instant loans, no checks\nURL:https://loans.example\nNOTE:call now\nEND:VCARD"

func TestContacts(t *testing.T) {
	e := build(t, &waE2E.Message{ContactsArrayMessage: &waE2E.ContactsArrayMessage{
		DisplayName: s("2 contacts"),
		Contacts: []*waE2E.ContactMessage{
			{DisplayName: s("Loans"), Vcard: s(bizCard)},
			{DisplayName: s("Me"), Vcard: s("BEGIN:VCARD\nFN:Me\nTEL;waid=966500000001:+966500000001\nEND:VCARD")},
		},
	}})
	if e.Kind != envelope.KindContact || len(e.Contacts) != 2 {
		t.Fatalf("kind %s with %d contacts, want 2 contact cards", e.Kind, len(e.Contacts))
	}
	c := e.Contacts[0]
	want := envelope.ContactRef{
		DisplayName: "Loans", FullName: "Easy Loans", Org: "Easy Loans LLC",
		Phones: []string{"+966 51 111 1111"}, WAIDs: []string{"966511111111"},
		BizName: "Easy Loans", BizDescription: "Instant loans, no checks", URLs: []string{"https://loans.example"},
	}
	if c.DisplayName != want.DisplayName || c.FullName != want.FullName || c.Org != want.Org || c.BizName != want.BizName ||
		c.BizDescription != want.BizDescription || !slices.Equal(c.WAIDs, want.WAIDs) || !slices.Equal(c.Phones, want.Phones) ||
		!slices.Equal(c.URLs, want.URLs) || c.IsSelfContact {
		t.Errorf("contact = %+v, want %+v", c, want)
	}
	if !e.Contacts[1].IsSelfContact {
		t.Error("a card with the sender's own number must be IsSelfContact")
	}
	wantText(t, e, envelope.SourceVCardBizDesc, "Instant loans, no checks")
	wantText(t, e, "vcard_biz_name", "Easy Loans")
	wantText(t, e, "vcard_note", "call now") // every field reaches the text pipeline
	wantText(t, e, "vcard_fn", "Easy Loans", "Me")
	wantText(t, e, "contacts_display_name", "2 contacts")
	wantURL(t, e, "vcard", "https://loans.example")

	many := &waE2E.ContactsArrayMessage{}
	for range maxContacts {
		many.Contacts = append(many.Contacts, &waE2E.ContactMessage{DisplayName: s("ok"), Vcard: s("FN:ok")})
	}
	many.Contacts = append(many.Contacts, &waE2E.ContactMessage{DisplayName: s("Spam Loans"), Vcard: s(bizCard)})
	res := Build(event(&waE2E.Message{ContactsArrayMessage: many}), bot)
	e = res.Envelope
	if len(e.Contacts) != maxContacts || !res.Truncated || e.Wrappers[len(e.Wrappers)-1] != envelope.WrapperTruncated {
		t.Errorf("%d contacts, truncated %v, wrappers %v; want the cap and a visible truncation mark", len(e.Contacts), res.Truncated, e.Wrappers)
	}
	if got := texts(e)["contact_display_name"]; got[len(got)-1] != "Spam Loans" {
		t.Errorf("display names = %v, want the dropped card's name kept as text", got)
	}
}

func TestChannelsInvitesPollsLocationsEventsCommerce(t *testing.T) {
	tests := []struct {
		name  string
		msg   *waE2E.Message
		kind  envelope.Kind
		check func(*testing.T, *envelope.Envelope)
	}{
		{"forwarded channel post", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: s("post"),
			ContextInfo: &waE2E.ContextInfo{ForwardedNewsletterMessageInfo: &waE2E.ContextInfo_ForwardedNewsletterMessageInfo{
				NewsletterJID: s("120363@newsletter"), NewsletterName: s("Signals VIP"),
			}},
		}}, envelope.KindText, func(t *testing.T, e *envelope.Envelope) {
			if len(e.Channels) != 1 || e.Channels[0] != (envelope.ChannelRef{JID: "120363@newsletter", Name: "Signals VIP", Source: "forwarded"}) {
				t.Errorf("Channels = %+v", e.Channels)
			}
			wantText(t, e, "channel_name", "Signals VIP")
		}},
		{"channel invite", &waE2E.Message{NewsletterFollowerInviteMessageV2: &waE2E.NewsletterFollowerInviteMessage{
			NewsletterJID: s("120363@newsletter"), NewsletterName: s("Signals"), Caption: s("follow"),
		}}, envelope.KindChannelInvite, func(t *testing.T, e *envelope.Envelope) {
			if len(e.Channels) != 1 || e.Channels[0].Source != "invite" || e.Channels[0].JID != "120363@newsletter" {
				t.Errorf("Channels = %+v", e.Channels)
			}
			wantText(t, e, "caption", "follow")
		}},
		{"group invite", &waE2E.Message{GroupInviteMessage: &waE2E.GroupInviteMessage{
			GroupJID: s("999@g.us"), InviteCode: s("AbC"), GroupName: s("Forex"), Caption: s("join"),
		}}, envelope.KindGroupInvite, func(t *testing.T, e *envelope.Envelope) {
			if len(e.Invites) != 1 || e.Invites[0] != (envelope.InviteRef{Code: "AbC", Group: "999@g.us", GroupName: "Forex", Source: "invite_message"}) {
				t.Errorf("Invites = %+v", e.Invites)
			}
			wantText(t, e, "invite_group_name", "Forex")
		}},
		{"poll v3", &waE2E.Message{PollCreationMessageV3: &waE2E.PollCreationMessage{
			Name:    s("Want profit?"),
			Options: []*waE2E.PollCreationMessage_Option{{OptionName: s("yes https://x.example")}, {OptionName: s("no")}},
		}}, envelope.KindPoll, func(t *testing.T, e *envelope.Envelope) {
			wantText(t, e, "poll_name", "Want profit?")
			wantText(t, e, envelope.SourcePollOption, "yes https://x.example", "no")
		}},
		{"poll v4 wrapper", &waE2E.Message{PollCreationMessageV4: &waE2E.FutureProofMessage{Message: &waE2E.Message{
			PollCreationMessage: &waE2E.PollCreationMessage{Name: s("q")},
		}}}, envelope.KindPoll, func(t *testing.T, e *envelope.Envelope) {
			wantText(t, e, "poll_name", "q")
			if !slices.Equal(e.Wrappers, []string{"PollCreationMessageV4"}) {
				t.Errorf("Wrappers = %v", e.Wrappers)
			}
		}},
		{"location", &waE2E.Message{LocationMessage: &waE2E.LocationMessage{
			Name: s("Shop"), Address: s("Main st"), URL: s("https://maps.example"), Comment: s("cheap"),
		}}, envelope.KindLocation, func(t *testing.T, e *envelope.Envelope) {
			wantText(t, e, "location_name", "Shop")
			wantText(t, e, "location_comment", "cheap")
			wantURL(t, e, "location", "https://maps.example")
		}},
		{
			"live location", &waE2E.Message{LiveLocationMessage: &waE2E.LiveLocationMessage{Caption: s("here")}},
			envelope.KindLiveLocation, func(t *testing.T, e *envelope.Envelope) { wantText(t, e, "caption", "here") },
		},
		{"event", &waE2E.Message{EventMessage: &waE2E.EventMessage{
			Name: s("Webinar"), Description: s("earn"), JoinLink: s("https://call.whatsapp.com/x"),
			Location: &waE2E.LocationMessage{Name: s("Online")},
		}}, envelope.KindEvent, func(t *testing.T, e *envelope.Envelope) {
			wantText(t, e, "event_name", "Webinar")
			wantText(t, e, "event_description", "earn")
			wantText(t, e, "event_location_name", "Online")
			wantURL(t, e, "event", "https://call.whatsapp.com/x")
		}},
		{"product", &waE2E.Message{ProductMessage: &waE2E.ProductMessage{
			Product: &waE2E.ProductMessage_ProductSnapshot{
				Title: s("Watch"), Description: s("replica"), URL: s("https://shop.example"),
				ProductImage: &waE2E.ImageMessage{FileSHA256: []byte{4}},
			},
			Body: s("buy"), Footer: s("dm"),
		}}, envelope.KindProduct, func(t *testing.T, e *envelope.Envelope) {
			wantText(t, e, "product_title", "Watch")
			wantText(t, e, "product_description", "replica")
			wantText(t, e, envelope.SourceBody, "buy")
			wantURL(t, e, "product", "https://shop.example")
			if len(e.Media) != 1 || e.Media[0].SHA256[0] != 4 {
				t.Errorf("Media = %+v, want the product image", e.Media)
			}
		}},
		{
			"order", &waE2E.Message{OrderMessage: &waE2E.OrderMessage{OrderTitle: s("Order"), Message: s("pay here")}},
			envelope.KindOrder, func(t *testing.T, e *envelope.Envelope) { wantText(t, e, "order_message", "pay here") },
		},
		{"payment request", &waE2E.Message{RequestPaymentMessage: &waE2E.RequestPaymentMessage{
			NoteMessage: &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: s("fee for prize")}},
		}}, envelope.KindPayment, func(t *testing.T, e *envelope.Envelope) {
			wantText(t, e, "payment_note_body", "fee for prize")
		}},
		{"buttons", &waE2E.Message{ButtonsMessage: &waE2E.ButtonsMessage{
			ContentText: s("Pick one"), FooterText: s("footer"),
			Buttons: []*waE2E.ButtonsMessage_Button{{ButtonID: s("b1"), ButtonText: &waE2E.ButtonsMessage_Button_ButtonText{DisplayText: s("Claim")}}},
		}}, envelope.KindInteractive, func(t *testing.T, e *envelope.Envelope) {
			wantText(t, e, "interactive", "Pick one", "footer", "Claim") // the button ID isn't text
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := build(t, tt.msg)
			if e.Kind != tt.kind {
				t.Errorf("Kind = %s, want %s", e.Kind, tt.kind)
			}
			tt.check(t, e)
		})
	}
}

func TestStatus(t *testing.T) {
	t.Run("status reshared into the group", func(t *testing.T) {
		src := waE2E.ContextInfo_IMAGE
		attr := waE2E.ContextInfo_RESHARED_FROM_POST_MANY_TIMES
		e := build(t, &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Caption: s("status"),
			ContextInfo: &waE2E.ContextInfo{
				ForwardOrigin:         waE2E.ContextInfo_STATUS.Enum(),
				StatusSourceType:      &src,
				StatusAttributionType: &attr,
				StatusAttributions: []*waStatusAttributions.StatusAttribution{
					{ActionURL: s("https://scam.example/a"), AttributionData: &waStatusAttributions.StatusAttribution_ExternalShare_{
						ExternalShare: &waStatusAttributions.StatusAttribution_ExternalShare{ActionURL: s("https://scam.example/b"), ActionFallbackURL: s("https://scam.example/c")},
					}},
					{AttributionData: &waStatusAttributions.StatusAttribution_StatusReshare_{StatusReshare: &waStatusAttributions.StatusAttribution_StatusReshare{
						Metadata: &waStatusAttributions.StatusAttribution_StatusReshare_Metadata{ChannelJID: s("120363@newsletter")},
					}}},
					{AttributionData: &waStatusAttributions.StatusAttribution_GroupStatus_{GroupStatus: &waStatusAttributions.StatusAttribution_GroupStatus{AuthorJID: s("42@lid")}}},
				},
			},
		}})
		st := e.Status
		if st == nil || !st.ForwardedFromStatus || st.SourceType != "IMAGE" || st.AttributionType != "RESHARED_FROM_POST_MANY_TIMES" || len(st.Attributions) != 3 {
			t.Fatalf("Status = %+v", st)
		}
		if a := st.Attributions[0]; a.ActionURL != "https://scam.example/a" || len(a.ExternalShareURLs) != 2 {
			t.Errorf("attribution 0 = %+v", a)
		}
		if st.Attributions[1].ChannelJID != "120363@newsletter" || st.Attributions[2].AuthorJID != "42@lid" {
			t.Errorf("attributions = %+v, want the reshared channel and the group status author", st.Attributions)
		}
		wantURL(t, e, "status_attribution", "https://scam.example/a", "https://scam.example/b", "https://scam.example/c")
		if len(e.Channels) != 1 || e.Channels[0].Source != "status_reshare" {
			t.Errorf("Channels = %+v, want the reshared channel for the channel analyzer", e.Channels)
		}
	})
	t.Run("reply quoting a status", func(t *testing.T) {
		e := build(t, &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: s("nice"),
			ContextInfo: &waE2E.ContextInfo{
				StanzaID: s("ST1"), Participant: s("888@lid"), RemoteJID: s("status@broadcast"),
				QuotedMessage: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: s("Invest $100 get $5000"), JPEGThumbnail: []byte{1}}},
			},
		}})
		if e.Quoted == nil || !e.Quoted.IsStatus() || e.Quoted.Kind != envelope.KindImage {
			t.Fatalf("Quoted = %+v, want a quoted status image", e.Quoted)
		}
		// The quoted status is shown to the whole group, so its content is analyzed too.
		wantText(t, e, "quoted_status_caption", "Invest $100 get $5000")
		if len(e.Media) != 1 || e.Media[0].Thumb[0] != 1 {
			t.Errorf("Media = %+v, want the quoted status thumbnail", e.Media)
		}
	})
	t.Run("group status post and status mention", func(t *testing.T) {
		e := build(t, &waE2E.Message{GroupStatusMessageV2: &waE2E.FutureProofMessage{Message: &waE2E.Message{
			StatusMentionMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{Conversation: s("crypto")}},
		}}})
		wantText(t, e, envelope.SourceBody, "crypto")
		if e.Status == nil || !e.Status.IsGroupStatus || !e.Status.IsStatusMention {
			t.Errorf("Status = %+v, want a group status post with a status mention", e.Status)
		}
		if !slices.Equal(e.Wrappers, []string{"GroupStatusMessageV2", "StatusMentionMessage"}) {
			t.Errorf("Wrappers = %v", e.Wrappers)
		}
	})
	t.Run("status quoted message", func(t *testing.T) {
		e := build(t, &waE2E.Message{StatusQuotedMessage: &waE2E.StatusQuotedMessage{Text: s("question?"), Thumbnail: []byte{3}}})
		wantText(t, e, "quoted_status_body", "question?")
		if e.Status == nil {
			t.Error("a status quoted message must set Status")
		}
	})
}

func TestEditsRevokesViewOnceUndecryptable(t *testing.T) {
	t.Run("edit", func(t *testing.T) {
		evt := event(&waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
			Type:          waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
			Key:           &waCommon.MessageKey{ID: s("ORIG")},
			EditedMessage: &waE2E.Message{Conversation: s("now spam")},
		}})
		evt.Info.ID, evt.Info.Edit, evt.IsEdit = "EDIT1", types.EditAttributeMessageEdit, true
		e := Build(evt, bot).Envelope
		if e == nil || !e.IsEdit || e.MsgID != "ORIG" || e.Kind != envelope.KindText {
			t.Fatalf("edit = %+v, want IsEdit with the original message ID (re-analysis and deletion use it)", e)
		}
		wantText(t, e, envelope.SourceBody, "now spam")
	})
	t.Run("raw edit wrapper", func(t *testing.T) {
		raw := &waE2E.Message{EditedMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
			Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), Key: &waCommon.MessageKey{ID: s("ORIG")},
			EditedMessage: &waE2E.Message{Conversation: s("x")},
		}}}}
		evt := event(nil)
		evt.RawMessage = raw
		e := Build(evt, bot).Envelope
		if e == nil || !e.IsEdit || e.MsgID != "ORIG" {
			t.Fatalf("edit from the raw message = %+v", e)
		}
	})
	for _, tt := range []struct {
		name       string
		edit       types.EditAttribute
		key        *waCommon.MessageKey
		wantSender wa.JID
		byAdmin    bool
	}{
		{"sender revoke", types.EditAttributeSenderRevoke, &waCommon.MessageKey{ID: s("GONE")}, "555@lid", false},
		{"admin revoke", types.EditAttributeAdminRevoke, &waCommon.MessageKey{ID: s("GONE"), Participant: s("777@lid")}, "777@lid", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			evt := event(&waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_REVOKE.Enum(), Key: tt.key}})
			evt.Info.Edit = tt.edit
			res := Build(evt, bot)
			want := wa.Revoke{Chat: "1203@g.us", Target: wa.MessageRef{Sender: tt.wantSender, ID: "GONE"}, By: "555@lid", ByAdmin: tt.byAdmin}
			if res.Envelope != nil || res.Revoke == nil || *res.Revoke != want {
				t.Errorf("revoke = %+v, %+v; want no envelope and %+v (it cancels pending actions)", res.Envelope, res.Revoke, want)
			}
		})
	}
	t.Run("sender revoke with a PN key", func(t *testing.T) {
		evt := event(&waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
			Type: waE2E.ProtocolMessage_REVOKE.Enum(), Key: &waCommon.MessageKey{ID: s("GONE"), Participant: s("966500000001@s.whatsapp.net")},
		}})
		evt.Info.Edit = types.EditAttributeSenderRevoke
		if r := Build(evt, bot).Revoke; r == nil || r.ByAdmin {
			t.Errorf("revoke = %+v, want a sender revoke: only the server's edit attribute says admin", r)
		}
	})
	t.Run("sender revoke naming someone else", func(t *testing.T) {
		evt := event(&waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
			Type: waE2E.ProtocolMessage_REVOKE.Enum(), Key: &waCommon.MessageKey{ID: s("ACCOMPLICE"), Participant: s("777@lid")},
		}})
		evt.Info.Edit = types.EditAttributeSenderRevoke
		if r := Build(evt, bot).Revoke; r != nil {
			t.Errorf("revoke = %+v, want none: a sender can only revoke their own message, so this can't cancel actions on 777's spam", r)
		}
	})
	t.Run("revoke without a key", func(t *testing.T) {
		evt := event(&waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_REVOKE.Enum()}})
		evt.Info.Edit = types.EditAttributeAdminRevoke
		if r := Build(evt, bot).Revoke; r != nil {
			t.Errorf("revoke = %+v, want none without a target ID", r)
		}
	})
	t.Run("unattested revoke beside content", func(t *testing.T) {
		evt := event(&waE2E.Message{
			Conversation:    s("BUY NOW https://scam.example"),
			ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_REVOKE.Enum(), Key: &waCommon.MessageKey{ID: s("OTHER")}},
		})
		res := Build(evt, bot)
		if res.Revoke != nil {
			t.Errorf("Revoke = %+v, want none: a revoke without the server's edit attribute can't cancel actions", res.Revoke)
		}
		if res.Envelope == nil || len(res.Envelope.Texts) != 1 {
			t.Errorf("Envelope = %+v, want the text beside the protocol message analyzed", res.Envelope)
		}
	})
	t.Run("view once", func(t *testing.T) {
		evt := event(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{FileSHA256: []byte{1}}})
		evt.IsViewOnce = true
		if e := Build(evt, bot).Envelope; !e.IsViewOnce || e.Kind != envelope.KindImage {
			t.Errorf("view-once = %+v, want IsViewOnce on the image kind", e)
		}
	})
	t.Run("undecryptable view once", func(t *testing.T) {
		evt := &events.UndecryptableMessage{Info: event(nil).Info, UnavailableType: events.UnavailableTypeViewOnce}
		e := BuildUndecryptable(evt, bot)
		if e.Kind != envelope.KindUndecryptable || !e.IsViewOnce || e.MsgID != "MSG1" || e.Sender.LID != "555@lid" || len(e.Texts) != 0 {
			t.Errorf("undecryptable = %+v, want a content-less envelope with sender, ID and IsViewOnce", e)
		}
	})
	t.Run("nothing to analyze", func(t *testing.T) {
		res := Build(event(&waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Text: s("👍")}}), bot)
		if res.Envelope != nil || res.Revoke != nil {
			t.Errorf("reaction = %+v, want no envelope", res)
		}
	})
}

func TestUnknownFieldsByReflection(t *testing.T) {
	saved := handlers["eventMessage"]
	delete(handlers, "eventMessage")
	defer func() { handlers["eventMessage"] = saved }()

	res := Build(event(&waE2E.Message{EventMessage: &waE2E.EventMessage{
		Name: s("Free money"), JoinLink: s("https://scam.example/join"),
		ContextInfo: &waE2E.ContextInfo{MentionedJID: []string{"1@lid"}},
	}}), bot)
	e := res.Envelope
	if e.Kind != envelope.KindUnknown || !slices.Equal(res.Unknown, []string{"EventMessage"}) {
		t.Fatalf("kind %s, unknown %v; want KindUnknown and the field counted", e.Kind, res.Unknown)
	}
	wantText(t, e, "unknown", "Free money")
	wantURL(t, e, "unknown", "https://scam.example/join")
	if len(e.Mentions) != 1 {
		t.Errorf("Mentions = %v, want the unknown type's ContextInfo read too", e.Mentions)
	}
}

// TestEveryMessageFieldClassified fails when a whatsmeow update adds a waE2E.Message field the
// builder has no decision for, or when a classified field no longer exists.
func TestEveryMessageFieldClassified(t *testing.T) {
	fields := (&waE2E.Message{}).ProtoReflect().Descriptor().Fields()
	seen := map[string]bool{}
	for i := range fields.Len() {
		fd := fields.Get(i)
		name := string(fd.Name())
		seen[name] = true
		_, h := handlers[name]
		_, g := generics[name]
		_, ig := ignored[name]
		w := isWrapperField(fd)
		n := 0
		for _, b := range []bool{h, g, ig, w} {
			if b {
				n++
			}
		}
		if n != 1 {
			t.Errorf("waE2E.Message.%s (%s) is in %d of handlers/generics/ignored/wrappers, want exactly 1: "+
				"handle it, read it generically or ignore it on purpose", name, fd.Message().FullName(), n)
		}
	}
	for _, m := range []map[string]bool{keys(handlers), keys(generics), keys(ignored)} {
		for name := range m {
			if !seen[name] {
				t.Errorf("%q is classified but isn't a waE2E.Message field", name)
			}
		}
	}
}

func keys[V any](m map[string]V) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

func TestQuotedNestingBounded(t *testing.T) {
	// Each quote quotes another; nested builds stop at maxNested.
	msg := &waE2E.Message{Conversation: s("leaf")}
	for range 20 {
		msg = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:        s("q"),
			ContextInfo: &waE2E.ContextInfo{StanzaID: s("x"), RemoteJID: s("status@broadcast"), QuotedMessage: msg},
		}}
	}
	e := build(t, msg)
	if n := len(e.Texts); n > maxNested+2 {
		t.Errorf("%d texts from a 20-deep quote chain, want at most %d (nesting must be bounded)", n, maxNested+2)
	}
	if strings.Contains(strings.Join(texts(e)[envelope.SourceBody], ""), "leaf") {
		t.Error("the leaf 20 quotes deep must not be reached")
	}
}
