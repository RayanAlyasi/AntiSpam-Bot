package inboxenv

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter/synth"
)

const bot wa.JID = "100@lid"

// TestDecodeMatchesLiveBuild stores events the way the connector does (proto of the raw message,
// JSON of the info) and checks the decoded envelope equals the one built from the live event.
func TestDecodeMatchesLiveBuild(t *testing.T) {
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	card := "BEGIN:VCARD\nVERSION:3.0\nFN:Deals\nTEL;type=CELL;waid=966500000001:+966 50 000 0001\nEND:VCARD"
	for _, spec := range []synth.Spec{
		{Chat: "120363000000000001@g.us", Sender: "200@lid", SenderPN: "966551234567@s.whatsapp.net", ID: "M1", Time: at, PushName: "Sam", Text: "hello http://example.com"},
		{Chat: "120363000000000001@g.us", Sender: "200@lid", ID: "M2", Time: at, VCards: []string{card}},
	} {
		evt, err := synth.Event(spec)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := proto.Marshal(evt.RawMessage)
		if err != nil {
			t.Fatal(err)
		}
		info, err := json.Marshal(evt.Info)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(KindMessage, raw, info, bot)
		if err != nil {
			t.Fatal(err)
		}
		want := adapter.Build(evt, bot)
		if !reflect.DeepEqual(got.Envelope, want.Envelope) {
			t.Fatalf("%s: decoded envelope\n%+v\nwant the live one\n%+v", spec.ID, got.Envelope, want.Envelope)
		}
		if got.Envelope.Sender.PN != spec.SenderPN || got.Envelope.Group != spec.Chat {
			t.Fatalf("%s: sender/group lost in the round trip: %+v", spec.ID, got.Envelope.Sender)
		}
	}
}

func TestDecodeRejectsCorruptRows(t *testing.T) {
	for name, tc := range map[string]struct {
		kind string
		raw  []byte
		info string
	}{
		"bad info":     {KindMessage, nil, "{"},
		"bad proto":    {KindMessage, []byte{0xff, 0xff, 0xff}, `{}`},
		"unknown kind": {"reaction", nil, `{}`},
	} {
		if _, err := Decode(tc.kind, tc.raw, json.RawMessage(tc.info), bot); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: err = %v, want ErrCorrupt (the worker must not retry it forever)", name, err)
		}
	}
	res, err := Decode(KindUndecryptable, nil, json.RawMessage(`{"Chat":"120363000000000001@g.us","Sender":"200@lid","ID":"U1"}`), bot)
	if err != nil || res.Envelope == nil || res.Envelope.MsgID != "U1" {
		t.Fatalf("undecryptable row: %+v, %v; want a content-less envelope with its ID", res.Envelope, err)
	}
}
