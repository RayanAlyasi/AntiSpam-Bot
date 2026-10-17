package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// whatsmeow marshals MessageInfo with its embedded MessageSource fields flattened and JIDs as
// strings (device suffixes included).
const groupInfoJSON = `{"Chat":"1@g.us","Sender":"30:4@lid","IsFromMe":false,"IsGroup":true,
	"AddressingMode":"lid","SenderAlt":"966530@s.whatsapp.net","ID":"M1","PushName":"Ali","Edit":""}`

func TestDecodeInfo(t *testing.T) {
	info, err := DecodeInfo(json.RawMessage(groupInfoJSON))
	if err != nil {
		t.Fatal(err)
	}
	if info.Chat != "1@g.us" || info.Sender != "30@lid" || info.SenderAlt != "966530@s.whatsapp.net" || info.ID != "M1" || !info.IsGroup {
		t.Errorf("DecodeInfo = %+v", info)
	}
	hosted, _ := DecodeInfo(json.RawMessage(`{"Chat":"1@g.us","Sender":"30:99@hosted.lid","SenderAlt":"966530:99@hosted"}`))
	if hosted.Sender != "30@lid" || hosted.SenderAlt != "966530@s.whatsapp.net" {
		t.Errorf("hosted sender decoded as %s / %s, want the plain LID and number (same person as the live event)", hosted.Sender, hosted.SenderAlt)
	}
	if _, err := DecodeInfo(json.RawMessage(`nope`)); !errors.Is(err, ErrPermanent) {
		t.Errorf("bad JSON: %v, want ErrPermanent (retrying won't fix it)", err)
	}
}

type recSink struct{ got []Route }

func (r *recSink) Handle(_ context.Context, _ Item, _ wa.MessageInfo, d Decision) error {
	r.got = append(r.got, d.Route)
	return nil
}

type recObserver struct {
	dm   []bool
	fail error
}

func (r *recObserver) ObserveSender(_ context.Context, _ wa.MessageInfo, dm bool) (int64, error) {
	r.dm = append(r.dm, dm)
	return 42, r.fail
}

type recIndex struct{ got []IndexEntry }

func (r *recIndex) Put(_ context.Context, e IndexEntry) error {
	r.got = append(r.got, e)
	return nil
}

func TestDispatcherRoutesToSinks(t *testing.T) {
	router := NewRouter(fakeGroups{"1@g.us": "active"}, fakeSet{"10@lid": true}, nil)
	mod, cmd, st, dm := &recSink{}, &recSink{}, &recSink{}, &recSink{}
	obs := &recObserver{}
	idx := &recIndex{}
	d := NewDispatcher(router, obs, idx, Sinks{Moderate: mod, Commands: cmd, Status: st, DM: dm}, zerolog.Nop())
	items := []string{
		groupInfoJSON,
		`{"Chat":"1@g.us","Sender":"10@lid","IsGroup":true,"ID":"A"}`,
		`{"Chat":"status@broadcast","Sender":"30@lid","ID":"S"}`,
		`{"Chat":"30@lid","Sender":"30@lid","ID":"D"}`,
		`{"Chat":"9@g.us","Sender":"30@lid","IsGroup":true,"ID":"U"}`,
	}
	for _, js := range items {
		if err := d.HandleInbox(t.Context(), Item{Info: json.RawMessage(js)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(mod.got) != 1 || len(cmd.got) != 2 || len(st.got) != 1 || len(dm.got) != 1 {
		t.Errorf("moderate %v, commands %v, status %v, dm %v; want 1, 2 (member + admin), 1, 1", mod.got, cmd.got, st.got, dm.got)
	}
	if len(idx.got) != 1 || idx.got[0].MsgID != "M1" || idx.got[0].SenderPerson != 42 || idx.got[0].Group != "1@g.us" {
		t.Errorf("index rows %+v: want one row for the moderated member message, with its sender", idx.got)
	}
	if len(obs.dm) != 4 || obs.dm[3] != true {
		t.Errorf("observed %v: want every routed sender recorded (not the ignored group), the DM as dm", obs.dm)
	}

	obs.fail = errors.New("db down")
	if err := d.HandleInbox(t.Context(), Item{Info: json.RawMessage(groupInfoJSON)}); err == nil {
		t.Error("observer failure swallowed; the row would be marked processed without its identity data")
	}
}
