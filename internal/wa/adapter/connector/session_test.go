package connector

import (
	"context"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// testSession builds a session without a whatsmeow client, for the event-handling paths.
func testSession(sink wa.EventSink, queueSize int) *session {
	return &session{
		bot: bot, sink: sink, log: zerolog.Nop(),
		queue: make(chan wa.Event, queueSize), control: make(chan wa.Event, 64),
		stop: make(chan struct{}), done: make(chan struct{}),
	}
}

type recSink struct {
	mu   sync.Mutex
	got  []wa.Event
	fail error
}

func (r *recSink) HandleEvent(_ context.Context, e wa.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, e)
	return r.fail
}

func TestOnEventNeverDropsAccountEvents(t *testing.T) {
	sink := &recSink{}
	s := testSession(sink, 1)
	group := &events.GroupInfo{JID: types.NewJID("1", types.GroupServer)}
	s.onEvent(group) // fills the queue
	s.onEvent(group) // dropped: the group refresh repairs group state
	s.onEvent(&events.TemporaryBan{Code: events.TempBanSentToTooManyPeople})
	s.onEvent(&events.Disconnected{})

	go s.drain()
	close(s.stop)
	<-s.done

	var kinds []string
	for _, e := range sink.got {
		switch e.(type) {
		case *wa.AccountEvent:
			kinds = append(kinds, "ban")
		case *wa.ConnectionEvent:
			kinds = append(kinds, "conn")
		case *wa.GroupEvent:
			kinds = append(kinds, "group")
		}
	}
	if len(kinds) != 3 || kinds[0] != "ban" || kinds[1] != "conn" || kinds[2] != "group" {
		t.Errorf("delivered %v, want the ban and connection events first and never dropped (the breaker depends on them), then the one queued group event", kinds)
	}
}

func TestOnEventAckFollowsTheInboxInsert(t *testing.T) {
	sink := &recSink{}
	s := testSession(sink, 1)
	msg := &events.Message{Info: types.MessageInfo{ID: "A"}}
	if !s.onEvent(msg) {
		t.Error("stored message: handler returned false, want true (ack)")
	}
	sink.fail = context.DeadlineExceeded
	if s.onEvent(msg) {
		t.Error("failed insert: handler returned true; the message would be acked without being stored")
	}
	if !s.onEvent(&events.Receipt{}) {
		t.Error("unused event: handler returned false, want true")
	}
}
