package policy

import "slices"

// ActionKind is a kind of planned action.
type ActionKind string

// Action kinds.
const (
	ActionDelete ActionKind = "delete"
	ActionBan    ActionKind = "ban"    // kick + rejoin block
	ActionReview ActionKind = "review" // ask the admins
)

// Queue priorities (DESIGN.md §10.2).
const (
	P0 = iota
	P1
	P2
	P3
	P4
)

// Action is one step of an action plan.
type Action struct {
	Kind     ActionKind
	Priority int
	Review   *ReviewRequest
}

// DetectionMode is the detections.mode value of a recorded decision.
type DetectionMode string

// Detection modes.
const (
	DetectionShadow        DetectionMode = "shadow"
	DetectionEnforce       DetectionMode = "enforce"
	DetectionNetworkRescan DetectionMode = "network_rescan"
)

// Plan is what happens with a decision in the group's current mode (DESIGN.md §8.4).
type Plan struct {
	Record  bool          // store a detection row
	Mode    DetectionMode // the detections.mode value
	Enqueue []Action      // what goes to the action queue: empty in shadow and paused
	Would   []Action      // what the decision calls for, stored as the detection's action_plan
}

// Actions lists the steps a decision calls for, in queue-priority order: delete (P1), ban
// (P2), review (P3).
func (d Decision) Actions() []Action {
	var out []Action
	if d.Delete {
		out = append(out, Action{Kind: ActionDelete, Priority: P1})
	}
	if d.Ban {
		out = append(out, Action{Kind: ActionBan, Priority: P2})
	}
	if d.Review != nil {
		r := *d.Review
		out = append(out, Action{Kind: ActionReview, Priority: P3, Review: &r})
	}
	return out
}

// PlanFor applies the group's mode to a decision. Enforce enqueues what the decision calls
// for; shadow records it with mode shadow and enqueues nothing; paused does nothing at all.
// Every non-clean verdict, and every decision with a route other than none, is recorded.
func PlanFor(s Settings, d Decision) Plan {
	if s.Mode == ModePaused {
		return Plan{}
	}
	would := d.Actions()
	record := d.Verdict != VerdictClean || (d.Route != RouteNone && d.Route != "")
	if s.Mode == ModeShadow {
		return Plan{Record: record, Mode: DetectionShadow, Would: would}
	}
	if s.Mode != ModeEnforce {
		// An unknown mode acts like shadow: nothing is done that the group didn't ask for.
		return Plan{Record: record, Mode: DetectionShadow, Would: would}
	}
	return Plan{Record: record, Mode: DetectionEnforce, Enqueue: slices.Clone(would), Would: would}
}
