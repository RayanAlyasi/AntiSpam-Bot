package wa

import "strings"

// Hosted (Cloud API) servers: a hosted device has its own server but the same user part as the
// plain phone number or LID, so it is the same person.
const (
	serverHosted    = "hosted"
	serverHostedLID = "hosted.lid"
)

// CanonicalJID turns a JID string as whatsmeow prints it ("user.agent:device@server") into the
// bot's form: no agent or device part, and hosted servers mapped to the plain ones. Every place
// that reads JIDs from whatsmeow data (live events, stored MessageInfo JSON) uses it, so one
// person always has one JID per identifier (DESIGN.md §7.7). Group, channel and broadcast JIDs
// pass through unchanged.
func CanonicalJID(s string) JID {
	s = strings.TrimSpace(s)
	user, server, ok := strings.Cut(s, "@")
	if !ok {
		return JID(s)
	}
	switch server {
	case ServerUser, ServerLID, serverHosted, serverHostedLID:
		if i := strings.IndexAny(user, ".:"); i >= 0 {
			user = user[:i]
		}
	}
	switch server {
	case serverHosted:
		server = ServerUser
	case serverHostedLID:
		server = ServerLID
	}
	return JID(user + "@" + server)
}
