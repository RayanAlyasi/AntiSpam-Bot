package wa

import "testing"

func TestCanonicalJID(t *testing.T) {
	tests := []struct{ in, want string }{
		{"123@lid", "123@lid"},
		{"123:5@lid", "123@lid"},
		{"9665.1:3@s.whatsapp.net", "9665@s.whatsapp.net"},
		{"9665:99@hosted", "9665@s.whatsapp.net"},
		{"123:99@hosted.lid", "123@lid"},
		{"120363-1600000000@g.us", "120363-1600000000@g.us"},
		{"status@broadcast", "status@broadcast"},
		{" 1@newsletter ", "1@newsletter"},
		{"nonsense", "nonsense"},
	}
	for _, tt := range tests {
		if got := CanonicalJID(tt.in); string(got) != tt.want {
			t.Errorf("CanonicalJID(%q) = %q, want %q (one person, one JID per identifier)", tt.in, got, tt.want)
		}
	}
}
