package envelope_test

import (
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const (
	modulePath  = "github.com/RayanAlyasi/AntiSpam-Bot"
	envelopePkg = modulePath + "/internal/envelope"
	waPkg       = modulePath + "/internal/wa"
)

func TestKindNamesAreStable(t *testing.T) {
	tests := []struct {
		kind envelope.Kind
		want string
	}{
		{envelope.KindUnknown, "unknown"},
		{envelope.KindText, "text"},
		{envelope.KindImage, "image"},
		{envelope.KindVideo, "video"},
		{envelope.KindPTV, "ptv"},
		{envelope.KindSticker, "sticker"},
		{envelope.KindStickerPack, "sticker_pack"},
		{envelope.KindDocument, "document"},
		{envelope.KindAudio, "audio"},
		{envelope.KindVoice, "voice"},
		{envelope.KindContact, "contact"},
		{envelope.KindLocation, "location"},
		{envelope.KindLiveLocation, "live_location"},
		{envelope.KindEvent, "event"},
		{envelope.KindPoll, "poll"},
		{envelope.KindGroupInvite, "group_invite"},
		{envelope.KindChannelInvite, "channel_invite"},
		{envelope.KindProduct, "product"},
		{envelope.KindOrder, "order"},
		{envelope.KindPayment, "payment"},
		{envelope.KindInteractive, "interactive"},
		{envelope.KindUndecryptable, "undecryptable"},
	}
	seen := map[envelope.Kind]string{}
	for _, tt := range tests {
		if got := string(tt.kind); got != tt.want {
			t.Errorf("Kind name = %q, want %q (Kind names are stable identifiers stored in goldens, metrics and evidence, contract F-024 / DESIGN §5.3)", got, tt.want)
		}
		if prev, dup := seen[tt.kind]; dup {
			t.Errorf("Kind %q is shared by %q and %q, want every Kind distinct (analyzers dispatch on Kind, DESIGN §6)", tt.kind, prev, tt.want)
		}
		seen[tt.kind] = tt.want
	}
}

func TestTextSourceNamesAreStable(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"SourceBody", envelope.SourceBody, "body"},
		{"SourceCaption", envelope.SourceCaption, "caption"},
		{"SourceLinkTitle", envelope.SourceLinkTitle, "link_title"},
		{"SourceVCardBizDesc", envelope.SourceVCardBizDesc, "vcard_biz_desc"},
		{"SourcePollOption", envelope.SourcePollOption, "poll_option"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q (TextSurface.Source names are fixed by DESIGN §5.3)", tt.name, tt.got, tt.want)
		}
	}
}

func TestForwardedManyTimes(t *testing.T) {
	tests := []struct {
		score int
		want  bool
	}{
		{0, false},
		{1, false},
		{4, false},
		{5, true},
		{127, true},
	}
	for _, tt := range tests {
		e := envelope.Envelope{IsForwarded: tt.score > 0, ForwardingScore: tt.score}
		if got := e.ForwardedManyTimes(); got != tt.want {
			t.Errorf("ForwardedManyTimes() with ForwardingScore=%d = %v, want %v (\"forwarded many times\" means ForwardingScore >= 5, DESIGN §5.3)", tt.score, got, tt.want)
		}
	}
}

func TestQuotedRefIsStatus(t *testing.T) {
	tests := []struct {
		name      string
		remoteJID wa.JID
		want      bool
	}{
		{"status broadcast", "status@broadcast", true},
		{"broadcast list", "1700000000@broadcast", false},
		{"group", "120363000000000000@g.us", false},
		{"person", "123456789@lid", false},
		{"same chat (empty RemoteJID)", "", false},
	}
	for _, tt := range tests {
		q := envelope.QuotedRef{ID: "3EB0ABC", Sender: "123456789@lid", RemoteJID: tt.remoteJID, Kind: envelope.KindImage}
		if got := q.IsStatus(); got != tt.want {
			t.Errorf("%s: QuotedRef{RemoteJID: %q}.IsStatus() = %v, want %v (a quote is a status reply exactly when RemoteJID == status@broadcast, DESIGN §5.3)", tt.name, tt.remoteJID, got, tt.want)
		}
	}
}

// fullEnvelope sets every §5.3 surface, with obfuscated Unicode in the raw texts.
func fullEnvelope() envelope.Envelope {
	return envelope.Envelope{
		Bot:   "966500000001@s.whatsapp.net",
		Group: "120363000000000000@g.us",
		Sender: envelope.Identity{
			LID:          "123456789@lid",
			PN:           "966500000002@s.whatsapp.net",
			PushName:     "Ahmed \u202Edab\u202C",
			VerifiedName: "Crypto Gains Ltd",
		},
		MsgID:           "3EB0C0FFEE",
		Kind:            envelope.KindImage,
		IsEdit:          true,
		IsViewOnce:      true,
		IsForwarded:     true,
		ForwardingScore: 7,
		Texts: []envelope.TextSurface{
			{Source: envelope.SourceBody, Raw: "f\u200Br\u200Be\u200Be 💰 \U000E0041 𝐟𝐫𝐞𝐞", Norm: "free"},
			{Source: envelope.SourceCaption, Raw: "اربح\u0640\u0640 المال", Norm: ""},
			{Source: envelope.SourceLinkTitle, Raw: "ＦＲＥＥ ｃｒｙｐｔｏ"},
			{Source: envelope.SourceVCardBizDesc, Raw: "Best\u00ADsignals"},
			{Source: envelope.SourcePollOption, Raw: "wa.me/966500000003"},
		},
		URLs: []envelope.URLRef{
			{URL: "hxxp://example[.]com/x", Source: "text"},
			{URL: "https://xn--pple-43d.com", Source: "preview"},
		},
		Media: []envelope.MediaRef{
			{Type: envelope.KindImage, SHA256: []byte{0xde, 0xad, 0xbe, 0xef}, Mimetype: "image/jpeg", Size: 123456, Thumb: []byte{0xff, 0xd8, 0xff}, Downloadable: true},
		},
		Contacts: []envelope.ContactRef{
			{
				DisplayName: "Support", FullName: "WhatsApp Support", Org: "Meta",
				Phones: []string{"+966 50 000 0004"}, WAIDs: []string{"966500000004"},
				BizName: "Gains", BizDescription: "Daily signals", URLs: []string{"https://gains.example"},
				IsSelfContact: true,
			},
		},
		Channels: []envelope.ChannelRef{
			{JID: "120363000000000001@newsletter", Name: "Free Signals", InviteCode: "0029VaAbCd", Source: "forwarded"},
		},
		Invites: []envelope.InviteRef{
			{Code: "AbCdEfGhIjK", Group: "120363000000000002@g.us", GroupName: "VIP", Source: "link"},
		},
		Mentions: []wa.JID{"111@lid", "222@lid"},
		Ads:      &envelope.AdRef{Title: "Earn now", Body: "Click to chat", SourceURL: "https://ads.example/x"},
		Quoted:   &envelope.QuotedRef{ID: "3EB0BEEF", Sender: "333@lid", RemoteJID: "status@broadcast", Kind: envelope.KindVideo},
		Status: &envelope.StatusRef{
			ForwardedFromStatus: true,
			SourceType:          "IMAGE",
			AttributionType:     "RESHARED_FROM_POST_MANY_TIMES",
			Attributions: []envelope.StatusAttribution{
				{Type: "STATUS_RESHARE", ActionURL: "https://act.example", ChannelJID: "120363000000000001@newsletter"},
				{Type: "EXTERNAL_SHARE", ExternalShareURLs: []string{"https://ext.example", "https://fallback.example"}},
				{Type: "GROUP_STATUS", AuthorJID: "444@lid"},
			},
			IsGroupStatus:   true,
			IsStatusMention: true,
		},
		Wrappers: []string{"GroupStatusMessageV2", "SpoilerMessage"},
	}
}

func TestEnvelopeJSONRoundTrip(t *testing.T) {
	in := fullEnvelope()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("json.Marshal(Envelope) error = %v, want nil (every envelope field must be JSON-encodable for golden tests, contract F-024)", err)
	}
	var out envelope.Envelope
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("json.Unmarshal(Envelope) error = %v, want nil (an encoded envelope must decode back, contract F-024)", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("Envelope after JSON round trip =\n%+v\nwant\n%+v\n(every field is exported and round-trips unchanged; Raw text keeps invisible and obfuscating characters as evidence, DESIGN §5.3/§7.2)", out, in)
	}
	if got, want := out.Texts[0].Raw, in.Texts[0].Raw; got != want {
		t.Errorf("Texts[0].Raw = %q, want %q byte for byte (Raw is evidence and is never normalized, DESIGN §7.2)", got, want)
	}
}

func TestZeroEnvelopeHasNoSurfaces(t *testing.T) {
	var e envelope.Envelope
	if e.Texts != nil || e.URLs != nil || e.Media != nil || e.Contacts != nil || e.Channels != nil ||
		e.Invites != nil || e.Mentions != nil || e.Wrappers != nil {
		t.Errorf("zero Envelope has non-nil surface slices, want all nil (envelope types carry no hidden defaults, contract F-024)")
	}
	if e.Ads != nil || e.Quoted != nil || e.Status != nil {
		t.Errorf("zero Envelope has non-nil Ads/Quoted/Status, want nil (absent references are nil, DESIGN §5.3)")
	}
	if e.ForwardedManyTimes() {
		t.Errorf("zero Envelope ForwardedManyTimes() = true, want false (ForwardingScore 0 is below the threshold of 5, DESIGN §5.3)")
	}
}

func TestEnvelopeHasNoWhatsmeowDependency(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("go tool not found in PATH: %v (needed to list the envelope package's dependencies)", err)
	}
	out, err := exec.Command(goBin, "list", "-deps", envelopePkg).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %s failed: %v\n%s", envelopePkg, err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		switch {
		case strings.HasPrefix(dep, "go.mau.fi/whatsmeow"):
			t.Errorf("envelope depends on %s, want no whatsmeow dependency (only internal/wa/adapter may import whatsmeow; analyzers never see protobufs, DESIGN §5.3)", dep)
		case strings.HasPrefix(dep, "google.golang.org/protobuf"):
			t.Errorf("envelope depends on %s, want no protobuf dependency (the envelope replaces raw protobufs, DESIGN §5.3)", dep)
		case strings.HasPrefix(dep, modulePath+"/") && dep != envelopePkg && dep != waPkg:
			t.Errorf("envelope depends on %s, want no internal package other than internal/wa (envelope is a leaf of plain types, contract F-024)", dep)
		}
	}
}
