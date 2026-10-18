package fastlane

import (
	"errors"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// TestLiveClientFollowsReconnects: writes go to whichever client the connector has for the bot
// now, and fail transiently while it has none.
func TestLiveClientFollowsReconnects(t *testing.T) {
	first, second := fake.New(), fake.New()
	var current wa.WAClient = first
	c := liveClient{bot: "100@lid", get: func(wa.JID) (wa.WAClient, bool) { return current, current != nil }}
	if err := c.Revoke(t.Context(), "1@g.us", wa.MessageRef{ID: "A"}); err != nil {
		t.Fatal(err)
	}
	current = second // a reconnect replaced the whatsmeow client
	if err := c.Revoke(t.Context(), "1@g.us", wa.MessageRef{ID: "B"}); err != nil {
		t.Fatal(err)
	}
	if len(first.CallsTo(fake.MethodRevoke)) != 1 || len(second.CallsTo(fake.MethodRevoke)) != 1 {
		t.Fatalf("revokes went to %d / %d clients' calls; want one each: a lane must not keep a dead client",
			len(first.CallsTo(fake.MethodRevoke)), len(second.CallsTo(fake.MethodRevoke)))
	}
	current = nil
	if err := c.Revoke(t.Context(), "1@g.us", wa.MessageRef{ID: "C"}); !errors.Is(err, wa.ErrTimeout) {
		t.Fatalf("revoke while disconnected = %v, want wa.ErrTimeout (transient: the executor retries)", err)
	}
}
