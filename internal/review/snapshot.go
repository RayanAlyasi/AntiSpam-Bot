package review

import (
	"net/http"
	"strings"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Sanitize makes message text safe to store: NUL bytes are dropped and invalid UTF-8 is replaced,
// because Postgres rejects both in text and jsonb columns. Without it, one crafted byte in a spam
// message would abort the detection's transaction, and the spam would never be deleted.
func Sanitize(s string) string {
	return strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "\uFFFD")
}

// PrepareSnapshot readies a snapshot for storage: the text is sanitized and redacted, a missing
// time becomes now, and the snapshot expires after EvidenceRetention (§14). Learning.Snapshot uses it, and a
// caller saving evidence in its own transaction (the detection pipeline) calls it directly.
func PrepareSnapshot(s Snapshot, now time.Time) Snapshot {
	if s.At.IsZero() {
		s.At = now
	}
	if !s.At.IsZero() {
		s.ExpiresAt = s.At.Add(EvidenceRetention)
	}
	s.Text = redact(Sanitize(s.Text))
	s.Reason = Sanitize(s.Reason)
	return s
}

// SnapshotOf is the evidence the bot keeps when it deletes a message (F-114): every text
// surface, redacted, and the first inline thumbnail (never full media), for 30 days counted from
// at, the time of the deletion. A zero at leaves At and ExpiresAt unset, which the store fills
// from its own clock. The result is ready to store.
func SnapshotOf(e *envelope.Envelope, detectionID int64, reason string, at time.Time) Snapshot {
	var texts []string
	for _, t := range e.Texts {
		// Each surface is redacted on its own, so digits in neighbouring surfaces never merge
		// into one "number".
		if s := strings.TrimSpace(t.Raw); s != "" {
			texts = append(texts, redact(Sanitize(s)))
		}
	}
	sender := e.Sender.LID
	if sender == "" {
		sender = e.Sender.PN
	}
	s := Snapshot{
		Group: e.Group, Message: wa.MessageRef{Sender: sender, ID: e.MsgID},
		Text: strings.Join(texts, "\n"), DetectionID: detectionID, Reason: reason, At: at,
	}
	for _, m := range e.Media {
		if len(m.Thumb) > 0 {
			// Most inline thumbnails are JPEG, but stickers carry a PNG one.
			mime := http.DetectContentType(m.Thumb)
			if !strings.HasPrefix(mime, "image/") {
				mime = "image/jpeg"
			}
			s.Thumbnail = &wa.Image{Data: m.Thumb, Mimetype: mime}
			break
		}
	}
	return PrepareSnapshot(s, at)
}
