package synth

import (
	"slices"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const (
	bot   wa.JID = "100@lid"
	group wa.JID = "120363000000000001@g.us"
	lid   wa.JID = "200@lid"
	pn    wa.JID = "966551234567@s.whatsapp.net"
)

func TestEnvelope(t *testing.T) {
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	card := "BEGIN:VCARD\nVERSION:3.0\nFN:Deals\nTEL;type=CELL;waid=966500000001:+966 50 000 0001\nEND:VCARD"
	tests := []struct {
		name     string
		spec     Spec
		wantKind envelope.Kind
		wantText string
		wantWAID string
	}{
		{"text", Spec{Chat: group, Sender: lid, SenderPN: pn, ID: "M1", Time: at, Text: "hello there"}, envelope.KindText, "hello there", ""},
		{"one vCard", Spec{Chat: group, Sender: lid, ID: "M2", Time: at, VCards: []string{card}}, envelope.KindContact, "", "966500000001"},
		{"two vCards", Spec{Chat: group, Sender: lid, ID: "M3", Time: at, Text: "deals", VCards: []string{card, card}}, envelope.KindContact, "", "966500000001"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := Envelope(tt.spec, bot)
			if err != nil {
				t.Fatal(err)
			}
			if e.Group != group || e.Sender.LID != lid || e.MsgID != tt.spec.ID || e.Bot != bot {
				t.Fatalf("envelope ids = group %s, sender %+v, id %s, bot %s; want the spec's", e.Group, e.Sender, e.MsgID, e.Bot)
			}
			if tt.spec.SenderPN != "" && e.Sender.PN != pn {
				t.Fatalf("sender PN = %q, want %q", e.Sender.PN, pn)
			}
			if e.Kind != tt.wantKind {
				t.Fatalf("kind = %s, want %s", e.Kind, tt.wantKind)
			}
			if tt.wantText != "" && !slices.ContainsFunc(e.Texts, func(s envelope.TextSurface) bool { return s.Raw == tt.wantText }) {
				t.Fatalf("texts %+v lack %q", e.Texts, tt.wantText)
			}
			if tt.wantWAID != "" && !slices.ContainsFunc(e.Contacts, func(c envelope.ContactRef) bool { return slices.Contains(c.WAIDs, tt.wantWAID) }) {
				t.Fatalf("contacts %+v lack waid %s", e.Contacts, tt.wantWAID)
			}
		})
	}
}
