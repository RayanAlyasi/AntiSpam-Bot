package actions

import (
	"strings"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// maskJID hides most of a phone-number JID for logs (GO-003): "966512345612@s.whatsapp.net"
// becomes "966•••••••12@s.whatsapp.net". Group, LID and other JIDs aren't phone numbers and are
// kept. It stands in until the shared logging helper (F-001) is available to this package.
func maskJID(j wa.JID) string {
	if j.Server() != wa.ServerUser {
		return string(j)
	}
	user := j.User()
	if len(user) <= 5 {
		return strings.Repeat("•", len(user)) + "@" + wa.ServerUser
	}
	return user[:3] + strings.Repeat("•", len(user)-5) + user[len(user)-2:] + "@" + wa.ServerUser
}
