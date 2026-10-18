package review_test

import (
	"testing"
	"unicode/utf8"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
)

// TestSnapshotSanitizes: a NUL or invalid UTF-8 byte in a spam message must never reach
// Postgres (it would abort the detection's transaction and the spam would stay).
func TestSnapshotSanitizes(t *testing.T) {
	e := &envelope.Envelope{Group: group, MsgID: "N1", Texts: []envelope.TextSurface{{Raw: "buy\x00 now \xff\xfe cheap"}}}
	s := review.SnapshotOf(e, 1, "detection", start)
	for _, r := range s.Text {
		if r == 0 {
			t.Fatalf("snapshot text %q keeps a NUL byte", s.Text)
		}
	}
	if !utf8.ValidString(s.Text) {
		t.Errorf("snapshot text %q isn't valid UTF-8", s.Text)
	}
	if got := review.Sanitize("a\x00b\xffc"); got != "ab�c" {
		t.Errorf("Sanitize = %q, want NUL dropped and the invalid byte replaced", got)
	}
}
