package inbox

import (
	"context"
	"errors"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

type fakeGroups map[wa.JID]string // "active" | "pending"

func (f fakeGroups) GroupState(_ context.Context, g wa.JID) (bool, bool, error) {
	if g == "err@g.us" {
		return false, false, errors.New("db down")
	}
	s, ok := f[g]
	return ok, s == "active", nil
}

type fakeSet map[wa.JID]bool

func (f fakeSet) IsGroupAdmin(_ context.Context, _, j wa.JID) (bool, error)  { return f[j], nil }
func (f fakeSet) IsAllowListed(_ context.Context, _, j wa.JID) (bool, error) { return f[j], nil }

func TestRoute(t *testing.T) {
	r := NewRouter(
		fakeGroups{"1@g.us": "active", "2@g.us": "pending"},
		fakeSet{"10@lid": true, "966510@s.whatsapp.net": true},
		fakeSet{"20@lid": true},
	)
	tests := []struct {
		name     string
		info     wa.MessageInfo
		want     Route
		moderate bool
		commands bool
	}{
		{"member in protected group", wa.MessageInfo{Chat: "1@g.us", Sender: "30@lid"}, RouteModerate, true, true},
		{"admin skips moderation", wa.MessageInfo{Chat: "1@g.us", Sender: "10@lid"}, RouteCommands, false, true},
		{"admin found by SenderAlt", wa.MessageInfo{Chat: "1@g.us", Sender: "11@lid", SenderAlt: "966510@s.whatsapp.net"}, RouteCommands, false, true},
		{"allow-listed skips moderation", wa.MessageInfo{Chat: "1@g.us", Sender: "20@lid"}, RouteCommands, false, true},
		{"bot's own group message", wa.MessageInfo{Chat: "1@g.us", Sender: "1@lid", IsFromMe: true}, RouteIgnore, false, false},
		{"unprotected group: commands only", wa.MessageInfo{Chat: "2@g.us", Sender: "30@lid"}, RouteCommands, false, true},
		{"untracked group ignored", wa.MessageInfo{Chat: "3@g.us", Sender: "30@lid"}, RouteIgnore, false, false},
		{"status: reputation only", wa.MessageInfo{Chat: StatusBroadcast, Sender: "30@lid"}, RouteStatus, false, false},
		{"own status", wa.MessageInfo{Chat: StatusBroadcast, IsFromMe: true}, RouteIgnore, false, false},
		{"DM by LID", wa.MessageInfo{Chat: "30@lid", Sender: "30@lid"}, RouteDM, false, true},
		{"DM by PN", wa.MessageInfo{Chat: "966530@s.whatsapp.net", Sender: "966530@s.whatsapp.net"}, RouteDM, false, true},
		{"own DM", wa.MessageInfo{Chat: "30@lid", IsFromMe: true}, RouteIgnore, false, false},
		{"broadcast list goes to DM handler", wa.MessageInfo{Chat: "123@broadcast", Sender: "30@lid"}, RouteDM, false, true},
		{"newsletter ignored", wa.MessageInfo{Chat: "1@newsletter"}, RouteIgnore, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := r.Route(t.Context(), tt.info)
			if err != nil {
				t.Fatal(err)
			}
			if d.Route != tt.want || d.Moderate() != tt.moderate || d.ParseCommands() != tt.commands {
				t.Errorf("Route = %s (%s) moderate=%v commands=%v; want %s moderate=%v commands=%v",
					d.Route, d.Reason, d.Moderate(), d.ParseCommands(), tt.want, tt.moderate, tt.commands)
			}
		})
	}
}

func TestRouteErrorsAreNotGuessed(t *testing.T) {
	r := NewRouter(fakeGroups{}, fakeSet{}, nil)
	if _, err := r.Route(t.Context(), wa.MessageInfo{Chat: "err@g.us", Sender: "1@lid"}); err == nil {
		t.Error("group-state failure: nil error, want it reported so the message is retried, not routed blindly")
	}
	// No allow-list configured: members are moderated.
	r = NewRouter(fakeGroups{"1@g.us": "active"}, fakeSet{}, nil)
	if d, _ := r.Route(t.Context(), wa.MessageInfo{Chat: "1@g.us", Sender: "1@lid"}); !d.Moderate() {
		t.Errorf("Route = %s, want moderate", d.Route)
	}
}
