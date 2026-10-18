package review_test

import (
	"strings"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
)

func TestSnapshotEdges(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n0000")
	e := &envelope.Envelope{
		Group: group, MsgID: "ST1", Media: []envelope.MediaRef{{Type: envelope.KindSticker, Thumb: png}},
		Texts: []envelope.TextSurface{{Raw: "call +966 55\n123 4567 today"}},
	}
	s := review.SnapshotOf(e, 1, "detection", start)
	if s.Thumbnail == nil || s.Thumbnail.Mimetype != "image/png" {
		t.Errorf("sticker thumbnail type = %v, want image/png", s.Thumbnail)
	}
	if strings.Contains(s.Text, "123 4567") {
		t.Errorf("snapshot text %q keeps a number split across lines, want it masked", s.Text)
	}
	if z := review.SnapshotOf(e, 1, "detection", time.Time{}); !z.ExpiresAt.IsZero() {
		t.Errorf("zero time gave expiry %v, want it left for the store to stamp", z.ExpiresAt)
	}
}
