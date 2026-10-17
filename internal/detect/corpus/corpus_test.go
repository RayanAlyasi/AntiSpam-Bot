package corpus

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/fingerprint"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/textnorm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
)

var key = []byte("0123456789abcdef-test-key")

func TestFromMessage(t *testing.T) {
	h, _ := blacklist.NewHasher(key)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	img := bytes.Repeat([]byte{7}, 32)
	e := &envelope.Envelope{
		Group: "120363000000000001@g.us",
		Texts: []envelope.TextSurface{
			{Source: "body", Raw: "Earn $500 a day! WhatsApp 055 123 4567, ask Sara or mail boss@scam.example. https://evil.com/join?utm_source=x"},
			{Source: "caption", Raw: "join chat.whatsapp.com/InvCode1"},
		},
		Media:    []envelope.MediaRef{{Type: envelope.KindImage, SHA256: img}, {Type: envelope.KindImage, SHA256: img}},
		Channels: []envelope.ChannelRef{{JID: "1203630001@newsletter"}},
	}
	textnorm.Prepare(e, "SA")
	ent := FromMessage(e, Options{
		Scope: ScopeGroup, GroupJID: string(e.Group), Status: StatusActive, Source: SourceReview,
		Region: "SA", Hasher: h, HashKey: key, Names: []string{"Sara"}, Keywords: []string{"earn"}, Confidence: 1, Now: now,
	})
	for _, leak := range []string{"551234567", "Sara", "boss@scam.example"} {
		if strings.Contains(ent.TextRawRedacted, leak) || strings.Contains(ent.TextNorm, leak) {
			t.Errorf("stored text keeps %q, want it redacted (§7.5 retention)", leak)
		}
	}
	if !strings.Contains(ent.TextRawRedacted, "[phone:") {
		t.Errorf("advertised number not kept as a keyed-hash token: %q", ent.TextRawRedacted)
	}
	if want, _ := hex.DecodeString(h.Hash("+966551234567")); len(ent.NumberHashes) != 1 || !bytes.Equal(ent.NumberHashes[0], want) {
		t.Errorf("NumberHashes = %x, want the keyed hash of the advertised number", ent.NumberHashes)
	}
	if ent.SimHash == nil || len(ent.MinHash) != fingerprint.MinHashBytes {
		t.Errorf("fingerprints missing: simhash %v minhash %d bytes", ent.SimHash, len(ent.MinHash))
	}
	if strings.Join(ent.Domains, ",") != "evil.com" || len(ent.URLs) != 2 {
		t.Errorf("Domains = %v URLs = %v, want evil.com and the two links", ent.Domains, ent.URLs)
	}
	if strings.Join(ent.InviteCodes, ",") != "InvCode1" || strings.Join(ent.ChannelJIDs, ",") != "1203630001@newsletter" {
		t.Errorf("InviteCodes = %v ChannelJIDs = %v", ent.InviteCodes, ent.ChannelJIDs)
	}
	if len(ent.MediaSHA256) != 1 {
		t.Errorf("MediaSHA256 = %d entries, want the duplicate image once", len(ent.MediaSHA256))
	}
	if ent.Kind != KindMessage || !ent.ExpiresAt.Equal(now.Add(Retention)) || ent.CreatedAt != now {
		t.Errorf("kind %s created %v expires %v", ent.Kind, ent.CreatedAt, ent.ExpiresAt)
	}
	keys := strings.Join(PatternKeys(&ent), " ")
	for _, want := range []string{"domain:evil.com", "number:" + h.Hash("+966551234567"), "invite:InvCode1", "channel:1203630001@newsletter", "sha256:" + hex.EncodeToString(img), "simhash:"} {
		if !strings.Contains(keys, want) {
			t.Errorf("PatternKeys = %s, missing %s", keys, want)
		}
	}
}

func TestFromMessageWithoutText(t *testing.T) {
	h, _ := blacklist.NewHasher(key)
	e := &envelope.Envelope{Media: []envelope.MediaRef{{Type: envelope.KindSticker, SHA256: []byte{1, 2}}}}
	ent := FromMessage(e, Options{Hasher: h, Now: time.Unix(0, 0), Retention: time.Hour})
	if ent.SimHash != nil || ent.MinHash != nil || ent.TextNorm != "" {
		t.Errorf("text fingerprints for a message without text: %+v", ent)
	}
	if !ent.ExpiresAt.Equal(time.Unix(0, 0).Add(time.Hour)) {
		t.Errorf("ExpiresAt = %v, want the custom retention", ent.ExpiresAt)
	}
	if keys := PatternKeys(&ent); len(keys) != 1 || keys[0] != "sha256:0102" {
		t.Errorf("PatternKeys = %v, want the sticker hash only", keys)
	}
}

func TestFromMessageKeepsNoNumbersInURLs(t *testing.T) {
	h, _ := blacklist.NewHasher(key)
	e := &envelope.Envelope{Texts: []envelope.TextSurface{{Source: "body", Raw: "chat https://wa.me/966551234567 or https://api.whatsapp.com/send?phone=966561234567 or https://evil.com/c?ref=966571234567"}}}
	textnorm.Prepare(e, "SA")
	ent := FromMessage(e, Options{Hasher: h, HashKey: key, Region: "SA", Now: time.Unix(0, 0)})
	all := strings.Join(ent.URLs, " ")
	for _, n := range []string{"551234567", "561234567", "571234567"} {
		if strings.Contains(all, n) {
			t.Errorf("URLs = %v keep a number in the clear (§7.5: keyed hashes only)", ent.URLs)
		}
	}
	if len(ent.URLs) != 1 || !strings.HasPrefix(ent.URLs[0], "https://evil.com/c") {
		t.Errorf("URLs = %v, want only the redacted non-WhatsApp link", ent.URLs)
	}
	if len(ent.NumberHashes) != 3 {
		t.Errorf("NumberHashes = %d, want all three advertised numbers as hashes", len(ent.NumberHashes))
	}
}
