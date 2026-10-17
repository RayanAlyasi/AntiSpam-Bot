package policy

// This file declares the public surface of the policy package (contract WP-I) so the
// acceptance tests compile. It has no logic; the implementation replaces it.

import "time"

// Preset is a named settings preset (DESIGN.md §12.2).
type Preset string

// Presets.
const (
	PresetRelaxed   Preset = "relaxed"
	PresetStandard  Preset = "standard"
	PresetStrict    Preset = "strict"
	PresetAskAdmins Preset = "ask_admins"
	PresetCustom    Preset = "custom"
)

// Mode is the group's run mode (§8.4).
type Mode string

// Modes.
const (
	ModeShadow  Mode = "shadow"
	ModeEnforce Mode = "enforce"
	ModePaused  Mode = "paused"
)

// EnforcementMode decides who makes ban decisions (§8.3).
type EnforcementMode string

// Enforcement modes.
const (
	EnforceAuto            EnforcementMode = "auto"
	EnforceAskDeleteFirst  EnforcementMode = "ask_delete_first"
	EnforceAskBeforeAction EnforcementMode = "ask_before_action"
)

// ContentType is a row of the auto-ban matrix (§9.2).
type ContentType string

// Content types.
const (
	TypeText            ContentType = "text"
	TypeLinks           ContentType = "links"
	TypeImages          ContentType = "images"
	TypeVideos          ContentType = "videos"
	TypeAudio           ContentType = "audio"
	TypeStickers        ContentType = "stickers"
	TypeDocuments       ContentType = "documents"
	TypeContacts        ContentType = "contacts"
	TypeChannelShares   ContentType = "channel_shares"
	TypeGroupInvites    ContentType = "group_invites"
	TypePolls           ContentType = "polls"
	TypeLocationsEvents ContentType = "locations_events"
	TypeStatus          ContentType = "status"
	TypeViewOnce        ContentType = "view_once"
)

// ContentTypes returns all content types in the §9.2 row order.
func ContentTypes() []ContentType { panic("not implemented") }

// CellAction is a matrix cell value (§9.2).
type CellAction string

// Cell actions.
const (
	CellAutoBan    CellAction = "auto_ban"
	CellAsk        CellAction = "ask"
	CellDeleteOnly CellAction = "delete_only"
	CellAllow      CellAction = "allow"
)

// Source is a verdict's confidence source (§7.3).
type Source string

// Sources.
const (
	SourceNone  Source = ""
	SourceKnown Source = "known"
	SourceRules Source = "rules"
	SourceAI    Source = "ai"
)

// Band is a certainty band (§7.0).
type Band string

// Bands.
const (
	BandClean          Band = "clean"
	BandCertain        Band = "certain"
	BandUncertain      Band = "uncertain"
	BandStillUncertain Band = "still_uncertain"
)

// ViolationAction is the reaction to a policy violation (§9.3).
type ViolationAction string

// Violation actions.
const (
	ViolationDeleteOnly ViolationAction = "delete_only"
	ViolationAsk        ViolationAction = "ask"
	ViolationBan        ViolationAction = "ban"
)

// Role is a member's group role.
type Role string

// Roles.
const (
	RoleMember     Role = "member"
	RoleAdmin      Role = "admin"
	RoleSuperAdmin Role = "superadmin"
)

// Cell is one auto-ban matrix cell.
type Cell struct {
	Action        CellAction `json:"action"`
	RequireSource []Source   `json:"require_source,omitempty"`
	Else          CellAction `json:"else,omitempty"`
}

// Effective returns the action the cell gives for evidence from src.
func (c Cell) Effective(src Source) CellAction { panic("not implemented") }

// Duration is a time.Duration that encodes as a Go duration string in JSON.
type Duration time.Duration

// TimeoutAction is the review timeout fallback (§9.5).
type TimeoutAction string

// Timeout actions.
const (
	TimeoutKeepDeletedNoBan TimeoutAction = "keep_deleted_no_ban"
	TimeoutDeleteNoBan      TimeoutAction = "delete_no_ban"
	TimeoutAutoBan          TimeoutAction = "auto_ban"
	TimeoutDismiss          TimeoutAction = "dismiss"
	TimeoutLeave            TimeoutAction = "leave"
)

// Settings is the per-group settings document (§12.1).
type Settings struct {
	Preset      Preset          `json:"preset"`
	Mode        Mode            `json:"mode"`
	Silent      bool            `json:"silent"`
	Enforcement Enforcement     `json:"enforcement"`
	Uncertain   Uncertain       `json:"uncertain"`
	Policy      ViolationPolicy `json:"policy"`
	Review      Review          `json:"review"`
}

// Enforcement is settings.enforcement.
type Enforcement struct {
	Mode          EnforcementMode      `json:"mode"`
	Established   Established          `json:"established"`
	Ban           Ban                  `json:"ban"`
	AutobanMatrix map[ContentType]Cell `json:"autoban_matrix"`
}

// Established is settings.enforcement.established.
type Established struct {
	MinDays          int `json:"min_days"`
	MinCleanMessages int `json:"min_clean_messages"`
}

// Ban is settings.enforcement.ban.
type Ban struct {
	RejoinBlock bool `json:"rejoin_block"`
}

// Uncertain is settings.uncertain.
type Uncertain struct {
	DeleteWhilePending bool `json:"delete_while_pending"`
}

// ViolationPolicy is settings.policy.
type ViolationPolicy struct {
	Action ViolationAction `json:"action"`
	Repeat *Repeat         `json:"repeat"`
}

// Repeat is settings.policy.repeat.
type Repeat struct {
	Count  int             `json:"count"`
	Window Duration        `json:"window"`
	Then   ViolationAction `json:"then"`
}

// Review is settings.review.
type Review struct {
	Timeout   Duration      `json:"timeout"`
	OnTimeout TimeoutAction `json:"on_timeout"`
}

// Defaults returns the default settings.
func Defaults() Settings { panic("not implemented") }

// PresetSettings returns Defaults with preset p applied.
func PresetSettings(p Preset) (Settings, error) { panic("not implemented") }

// ApplyPreset resets the matrix and the preset's extras to preset p.
func (s *Settings) ApplyPreset(p Preset) error { panic("not implemented") }

// Validate reports whether the settings are acceptable.
func (s Settings) Validate() error { panic("not implemented") }

// Signal is one scoring signal as seen by the decision engine.
type Signal struct {
	Code   string
	Weight float64
	Prior  bool
}

// Violation is a hard policy violation (§9.3).
type Violation struct {
	Rule   string
	Detail string
}

// Finding is a scored verdict for one message.
type Finding struct {
	Band       Band
	Source     Source
	Score      float64
	Type       ContentType
	Signals    []Signal
	Violations []Violation
	At         time.Time
}

// Standing is the sender's standing in the group.
type Standing struct {
	Role             Role
	IsBot            bool
	Allowlisted      bool
	Established      bool
	Compromised      bool
	New              bool
	Trust            float64
	RecentViolations []time.Time
}

// Route is the §8.3 route taken.
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

// Verdict is the detection verdict.
type Verdict string

// Verdicts.
const (
	VerdictSpam   Verdict = "spam"
	VerdictPolicy Verdict = "policy"
	VerdictClean  Verdict = "clean"
)

// ReviewKind is the kind of review item.
type ReviewKind string

// Review kinds.
const (
	ReviewBanDecision ReviewKind = "ban_decision"
	ReviewUncertain   ReviewKind = "uncertain"
	ReviewHacked      ReviewKind = "hacked"
	ReviewAskBefore   ReviewKind = "ask_before"
	ReviewAlert       ReviewKind = "alert"
)

// ReviewRequest asks admins to decide.
type ReviewRequest struct {
	Kind       ReviewKind
	ProposeBan bool
	Timeout    time.Duration
	OnTimeout  TimeoutAction
}

// Decision is the outcome of Decide.
type Decision struct {
	Verdict Verdict
	Route   Route
	Delete  bool
	Ban     bool
	Review  *ReviewRequest
	Reason  string
}

// Decide routes a finding for a sender under the group's settings.
func Decide(s Settings, f Finding, st Standing) Decision { panic("not implemented") }

// ActionKind is a queued action kind.
type ActionKind string

// Action kinds.
const (
	ActionDelete ActionKind = "delete"
	ActionBan    ActionKind = "ban"
	ActionReview ActionKind = "review"
)

// Action is one planned action.
type Action struct {
	Kind     ActionKind
	Priority int
	Review   *ReviewRequest
}

// DetectionMode is the detections.mode value.
type DetectionMode string

// Detection modes.
const (
	DetectionShadow  DetectionMode = "shadow"
	DetectionEnforce DetectionMode = "enforce"
)

// Plan is what to record and enqueue for a decision.
type Plan struct {
	Record  bool
	Mode    DetectionMode
	Enqueue []Action
	Would   []Action
}

// PlanFor turns a decision into a plan under the group's run mode.
func PlanFor(s Settings, d Decision) Plan { panic("not implemented") }
