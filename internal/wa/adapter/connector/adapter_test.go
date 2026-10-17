package connector

import (
	"errors"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types/events"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter"
)

func TestSessionFeedsBansToTheAdapter(t *testing.T) {
	s := testSession(&recSink{}, 4)
	s.client = adapter.New(nil)
	if s.Client() == nil {
		t.Fatal("Client() is nil, want the adapter")
	}
	s.onEvent(&events.TemporaryBan{Code: events.TempBanSentToTooManyPeople, Expire: time.Hour})
	err := s.Client().Revoke(t.Context(), "1@g.us", wa.MessageRef{Sender: "1@lid", ID: "X"})
	if !errors.Is(err, wa.ErrTemporaryBan) {
		t.Errorf("Revoke during a ban: %v, want wa.ErrTemporaryBan (the executor opens its breaker on it)", err)
	}
}
