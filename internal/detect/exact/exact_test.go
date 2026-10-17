package exact

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/ruleview"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/textnorm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
)

const grp = "120363000000000001@g.us"

var (
	spamImage   = bytes.Repeat([]byte{0xab}, 32)
	spamSticker = bytes.Repeat([]byte{0xcd}, 32)
	okImage     = bytes.Repeat([]byte{0x01}, 32)
)

func TestAnalyzer(t *testing.T) {
	hasher, err := blacklist.NewHasher([]byte("0123456789abcdef-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	fp := func(kind rules.FingerprintKind, v string, label rules.Label) rules.Fingerprint {
		return rules.Fingerprint{Kind: kind, Value: v, Label: label, Source: rules.SourceAdmin, Scope: rules.ScopeGlobal}
	}
	l := rules.StaticLoader{
		Rules: []rules.Rule{
			{ID: 1, Scope: rules.ScopeGlobal, Type: rules.TypeChannel, Pattern: "1203630001@newsletter", NormPattern: "1203630001@newsletter", ActionClass: rules.ActionHard, Enabled: true},
			{ID: 2, Scope: rules.ScopeGlobal, Type: rules.TypeInvite, Pattern: "InvWatch", NormPattern: "InvWatch", ActionClass: rules.ActionWeighted, Weight: 1.5, Enabled: true},
			{ID: 3, Scope: rules.ScopeGlobal, Type: rules.TypePhone, Pattern: "+966561234567", NormPattern: hasher.Hash("+966561234567"), ActionClass: rules.ActionHard, Enabled: true},
		},
		Fingerprints: []rules.Fingerprint{
			fp(rules.FPSHA256, hex.EncodeToString(spamImage), rules.LabelSpam),
			fp(rules.FPSHA256, hex.EncodeToString(spamSticker), rules.LabelSpam),
			fp(rules.FPInvite, "InvSpam", rules.LabelSpam),
			fp(rules.FPNumber, hasher.Hash("+966551234567"), rules.LabelSpam),
			fp(rules.FPChannel, "0029VaChan", rules.LabelSpam),
		},
	}
	h := rules.NewHolder(l, clock.Real{}, zerolog.Nop())
	if err := h.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	a := New[struct{}](ruleview.Source{Rules: h}, hasher.Hash)
	tests := []struct {
		name  string
		e     envelope.Envelope
		codes string
		known bool
	}{
		{"known image hash", envelope.Envelope{Media: []envelope.MediaRef{{Type: envelope.KindImage, SHA256: spamImage}}}, CodeMediaKnown, true},
		{"known sticker hash", envelope.Envelope{Media: []envelope.MediaRef{{Type: envelope.KindSticker, SHA256: spamSticker}}}, CodeStickerKnown, true},
		{"unknown image", envelope.Envelope{Media: []envelope.MediaRef{{Type: envelope.KindImage, SHA256: okImage}}}, "", false},
		{"short hash doesn't panic", envelope.Envelope{Media: []envelope.MediaRef{{Type: envelope.KindImage, SHA256: []byte{1}}}}, "", false},
		{"channel forward", envelope.Envelope{Channels: []envelope.ChannelRef{{JID: "1203630001@newsletter", Source: "forwarded"}}}, CodeChannelKnown, true},
		{"channel link", envelope.Envelope{Texts: []envelope.TextSurface{{Source: "body", Raw: "follow https://whatsapp.com/channel/0029VaChan"}}}, CodeChannelKnown, true},
		{"invite fingerprint", envelope.Envelope{Invites: []envelope.InviteRef{{Code: "InvSpam", Source: "invite_message"}}}, CodeInviteKnown, true},
		{"invite link on a watch list", envelope.Envelope{Texts: []envelope.TextSurface{{Source: "body", Raw: "join chat.whatsapp.com/InvWatch"}}}, CodeInviteListed, false},
		{"advertised number in text", envelope.Envelope{Texts: []envelope.TextSurface{{Source: "body", Raw: "whatsapp 055 123 4567"}}}, CodeNumberKnown, true},
		{"number rule via wa.me", envelope.Envelope{URLs: []envelope.URLRef{{URL: "https://wa.me/966561234567", Source: "text"}}}, CodeNumberKnown, true},
		{"number in vcard", envelope.Envelope{Contacts: []envelope.ContactRef{{WAIDs: []string{"966551234567"}}}}, CodeNumberKnown, true},
		{"ordinary message", envelope.Envelope{Texts: []envelope.TextSurface{{Source: "body", Raw: "call me at 0501112233 about the car"}}}, "", false},
		{"two images one code", envelope.Envelope{Media: []envelope.MediaRef{{Type: envelope.KindImage, SHA256: spamImage}, {Type: envelope.KindVideo, SHA256: spamImage}}}, CodeMediaKnown, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := tt.e
			e.Group = grp
			textnorm.Prepare(&e, "SA")
			sigs, err := a.Analyze(t.Context(), &e, &detect.State{})
			if err != nil {
				t.Fatal(err)
			}
			var codes []string
			for _, s := range sigs {
				codes = append(codes, s.Code)
				if s.Known != tt.known || s.Weight <= 0 || s.Evidence == "" {
					t.Errorf("signal %+v, want Known=%v with weight and evidence (§7.0)", s, tt.known)
				}
				if strings.Contains(s.Evidence, "551234567") || strings.Contains(s.Evidence, "561234567") {
					t.Errorf("evidence %q shows a full number", s.Evidence)
				}
			}
			if got := strings.Join(codes, ","); got != tt.codes {
				t.Errorf("codes = %q, want %q", got, tt.codes)
			}
		})
	}
}
