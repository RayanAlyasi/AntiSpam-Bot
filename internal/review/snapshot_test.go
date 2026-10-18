package review_test

import (
	"strings"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
)

// TestSnapshotOf covers F-114's evidence for a deletion: every text surface redacted, the
// inline thumbnail (not the media itself), 30-day expiry.
func TestSnapshotOf(t *testing.T) {
	e := &envelope.Envelope{
		Group: group, MsgID: "DEL1", Sender: envelope.Identity{LID: ahmed},
		Texts: []envelope.TextSurface{{Source: "caption", Raw: "call +966 55 123 4567"}, {Source: "link_title", Raw: "  "}, {Source: "vcard", Raw: "Ahmed Cars"}},
		Media: []envelope.MediaRef{{Type: envelope.KindImage, SHA256: []byte{1}}, {Type: envelope.KindImage, Thumb: []byte{9, 9}}},
	}
	s := review.SnapshotOf(e, 77, "detection", start)
	if s.Group != group || s.Message.ID != "DEL1" || s.Message.Sender != ahmed || s.DetectionID != 77 || s.Reason != "detection" {
		t.Errorf("snapshot = %+v, want the deleted message's group, ID, sender and detection", s)
	}
	if strings.Contains(s.Text, "123 4567") || !strings.Contains(s.Text, "call") || !strings.Contains(s.Text, "Ahmed Cars") {
		t.Errorf("snapshot text %q: want every surface kept with numbers masked (DESIGN §14)", s.Text)
	}
	if s.Thumbnail == nil || len(s.Thumbnail.Data) != 2 {
		t.Error("snapshot has no thumbnail, want the inline thumbnail")
	}
	if !s.ExpiresAt.Equal(start.Add(30 * 24 * time.Hour)) {
		t.Errorf("expires %v, want 30 days after the deletion", s.ExpiresAt)
	}
	pn := &envelope.Envelope{Group: group, MsgID: "DEL2", Sender: envelope.Identity{PN: "966550000000@s.whatsapp.net"}}
	if s := review.SnapshotOf(pn, 0, "detection", start); s.Message.Sender != "966550000000@s.whatsapp.net" || s.Thumbnail != nil {
		t.Errorf("phone-only sender snapshot = %+v, want the phone JID and no thumbnail", s)
	}
}
