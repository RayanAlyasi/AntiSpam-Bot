package envelope

import (
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

func TestForwardedManyTimes(t *testing.T) {
	tests := []struct {
		score int
		want  bool
	}{{0, false}, {4, false}, {5, true}, {6, true}, {-1, false}}
	for _, tt := range tests {
		if got := (Envelope{ForwardingScore: tt.score}).ForwardedManyTimes(); got != tt.want {
			t.Errorf("score %d: ForwardedManyTimes() = %v, want %v", tt.score, got, tt.want)
		}
	}
}

func TestQuotedIsStatus(t *testing.T) {
	tests := []struct {
		remote wa.JID
		want   bool
	}{
		{"status@broadcast", true},
		{"123@broadcast", false},
		{"1203@g.us", false},
		{"", false},
		{"status@s.whatsapp.net", false},
	}
	for _, tt := range tests {
		if got := (QuotedRef{RemoteJID: tt.remote}).IsStatus(); got != tt.want {
			t.Errorf("%q: IsStatus() = %v, want %v", tt.remote, got, tt.want)
		}
	}
}
