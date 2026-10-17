package wa

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestJIDParts(t *testing.T) {
	tests := []struct {
		jid            JID
		user, server   string
		isGroup, isLID bool
	}{
		{"123456@lid", "123456", ServerLID, false, true},
		{"966500000000@s.whatsapp.net", "966500000000", ServerUser, false, false},
		{"120363000000000000@g.us", "120363000000000000", ServerGroup, true, false},
		{"status@broadcast", "status", ServerBroadcast, false, false},
		{"no-server", "no-server", "", false, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.jid), func(t *testing.T) {
			if got := tt.jid.User(); got != tt.user {
				t.Errorf("User() = %q, want %q", got, tt.user)
			}
			if got := tt.jid.Server(); got != tt.server {
				t.Errorf("Server() = %q, want %q", got, tt.server)
			}
			if got := tt.jid.IsGroup(); got != tt.isGroup {
				t.Errorf("IsGroup() = %v, want %v", got, tt.isGroup)
			}
			if got := tt.jid.IsLID(); got != tt.isLID {
				t.Errorf("IsLID() = %v, want %v (people are keyed by LID)", got, tt.isLID)
			}
		})
	}
}

func TestTemporaryBanErrorMatchesSentinel(t *testing.T) {
	err := fmt.Errorf("send: %w", &TemporaryBanError{Code: 101, Expire: time.Hour})
	if !errors.Is(err, ErrTemporaryBan) {
		t.Fatalf("errors.Is(%v, ErrTemporaryBan) = false, want true (the executor opens the breaker on it)", err)
	}
	var ban *TemporaryBanError
	if !errors.As(err, &ban) || ban.Code != 101 {
		t.Fatalf("errors.As gave %+v, want code 101", ban)
	}
	if errors.Is(err, ErrRateLimited) {
		t.Fatalf("a temporary ban must not look like a rate limit")
	}
}
