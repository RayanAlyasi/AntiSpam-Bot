// Package pipeline is the fast lane (DESIGN.md §4, §7.1): one inbox event becomes an envelope,
// is normalized, runs through the T0/T1 analyzers and scoring, gets a certainty band and a
// route, and is written as a detection plus enqueued actions in one Postgres transaction.
package pipeline

import "github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"

// Mode is a group's operating mode (DESIGN.md §8.4, §12.1).
type Mode string

// Modes.
const (
	ModeEnforce Mode = "enforce"
	ModeShadow  Mode = "shadow"
	ModePaused  Mode = "paused"
)

// Band is the certainty band of a verdict (DESIGN.md §7.0).
type Band string

// Bands.
const (
	BandClean          Band = "clean"
	BandCertain        Band = "certain"
	BandUncertain      Band = "uncertain"
	BandStillUncertain Band = "still_uncertain"
)

// Source is the confidence source of a verdict (DESIGN.md §7.3).
type Source string

// Confidence sources.
const (
	SourceNone  Source = ""
	SourceKnown Source = "known"
	SourceRules Source = "rules"
	SourceAI    Source = "ai"
)

// Route is the routing decision (DESIGN.md §8.3, §14 detections.route).
type Route string

// Routes.
const (
	RouteNone       Route = "none"
	RouteAutoBan    Route = "auto_ban"
	RouteAsk        Route = "ask"
	RouteDeleteOnly Route = "delete_only"
	RouteAllow      Route = "allow"
	RoutePolicy     Route = "policy"
)

// ActionKind is one planned action of a verdict.
type ActionKind string

// Action kinds.
const (
	ActionDelete ActionKind = "delete"
	ActionBan    ActionKind = "ban"
	ActionReview ActionKind = "review"
)

// Verdict is what the fast lane decided for one message.
type Verdict struct {
	Group       wa.JID
	Message     wa.MessageID
	Sender      wa.JID
	Mode        Mode
	Band        Band
	Source      Source
	Route       Route
	Actions     []ActionKind // what the bot does, or would do in shadow mode: delete, ban, review
	Explanation string       // the signals behind the verdict, for admins
	// Duplicate is set when the message had already been processed (a redelivery): nothing
	// was recorded or enqueued again.
	Duplicate bool
	// DetectionID is the stored detection's ID (0 when none was recorded), for logs.
	DetectionID int64
}
