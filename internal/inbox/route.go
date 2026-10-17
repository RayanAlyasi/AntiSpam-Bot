package inbox

import (
	"context"
	"fmt"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Route is where a received message goes (DESIGN.md §5.1).
type Route string

// Routes.
const (
	// RouteModerate: a member's message in a protected group. It is analysed and may be acted on,
	// and is also parsed for commands.
	RouteModerate Route = "moderate"
	// RouteCommands: a message in a group from the bot's admins-or-trusted set (group admins,
	// allow-listed members) or in a tracked but unprotected group. Never moderated; only parsed
	// for commands, which check their own permissions.
	RouteCommands Route = "commands"
	// RouteStatus: a status@broadcast post. Analysed for the author's person reputation only,
	// never acted on.
	RouteStatus Route = "status"
	// RouteDM: a direct message (or a broadcast-list message, which shows up in the sender's DM
	// thread). Goes to the command/auth/review handler; spam only updates reputation.
	RouteDM Route = "dm"
	// RouteIgnore: the bot's own messages and chats the bot doesn't track.
	RouteIgnore Route = "ignore"
)

// Decision is a routing decision with the reason, for logs and tests.
type Decision struct {
	Route  Route
	Reason string
}

// Moderate reports whether the message goes through detection with possible actions.
func (d Decision) Moderate() bool { return d.Route == RouteModerate }

// ParseCommands reports whether the message is parsed for commands.
func (d Decision) ParseCommands() bool {
	return d.Route == RouteModerate || d.Route == RouteCommands || d.Route == RouteDM
}

// GroupState says whether a group is tracked and whether it is protected (the bot is an admin
// there and moderation is on).
type GroupState interface {
	GroupState(ctx context.Context, group wa.JID) (tracked, moderated bool, err error)
}

// AdminCheck says whether a JID (LID or phone number) is an admin of a group.
type AdminCheck interface {
	IsGroupAdmin(ctx context.Context, group, jid wa.JID) (bool, error)
}

// AllowList says whether a JID is an allow-listed member of a group.
type AllowList interface {
	IsAllowListed(ctx context.Context, group, jid wa.JID) (bool, error)
}

// Router decides where each received message goes.
type Router struct {
	groups GroupState
	admins AdminCheck
	allow  AllowList
}

// NewRouter builds a router. allow may be nil (nobody is allow-listed).
func NewRouter(groups GroupState, admins AdminCheck, allow AllowList) *Router {
	return &Router{groups: groups, admins: admins, allow: allow}
}

// StatusBroadcast is the chat of status posts.
const StatusBroadcast wa.JID = "status@broadcast"

// Route classifies one message. An error means the decision couldn't be made safely; the caller
// retries later rather than guessing (a wrong guess would either moderate an admin or skip a
// spammer).
func (r *Router) Route(ctx context.Context, info wa.MessageInfo) (Decision, error) {
	chat := info.Chat
	switch {
	case chat == StatusBroadcast:
		if info.IsFromMe {
			return Decision{RouteIgnore, "own status"}, nil
		}
		return Decision{RouteStatus, "status post: reputation only"}, nil
	case chat.Server() == wa.ServerBroadcast:
		if info.IsFromMe {
			return Decision{RouteIgnore, "own broadcast"}, nil
		}
		return Decision{RouteDM, "broadcast-list message: DM handler"}, nil
	case chat.Server() == wa.ServerUser || chat.Server() == wa.ServerLID:
		if info.IsFromMe {
			return Decision{RouteIgnore, "own DM"}, nil
		}
		return Decision{RouteDM, "direct message"}, nil
	case !chat.IsGroup():
		return Decision{RouteIgnore, "unsupported chat " + chat.Server()}, nil
	}

	if info.IsFromMe {
		return Decision{RouteIgnore, "the bot's own message"}, nil
	}
	tracked, moderated, err := r.groups.GroupState(ctx, chat)
	if err != nil {
		return Decision{}, fmt.Errorf("route: group state of %s: %w", chat, err)
	}
	if !tracked {
		return Decision{RouteIgnore, "group not tracked"}, nil
	}
	if !moderated {
		return Decision{RouteCommands, "group not protected"}, nil
	}

	senders := []wa.JID{info.Sender}
	if info.SenderAlt != "" {
		senders = append(senders, info.SenderAlt)
	}
	for _, s := range senders {
		admin, err := r.admins.IsGroupAdmin(ctx, chat, s)
		if err != nil {
			return Decision{}, fmt.Errorf("route: admin check: %w", err)
		}
		if admin {
			return Decision{RouteCommands, "sender is a group admin"}, nil
		}
	}
	if r.allow != nil {
		for _, s := range senders {
			ok, err := r.allow.IsAllowListed(ctx, chat, s)
			if err != nil {
				return Decision{}, fmt.Errorf("route: allow-list check: %w", err)
			}
			if ok {
				return Decision{RouteCommands, "sender is allow-listed"}, nil
			}
		}
	}
	return Decision{RouteModerate, "member message in a protected group"}, nil
}
