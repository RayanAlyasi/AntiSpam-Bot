package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// Preset is a named bundle of matrix cells and extras (DESIGN.md §12.2).
type Preset string

// Presets. Custom keeps whatever the group set and is compared against Standard.
const (
	PresetRelaxed   Preset = "relaxed"
	PresetStandard  Preset = "standard"
	PresetStrict    Preset = "strict"
	PresetAskAdmins Preset = "ask_admins"
	PresetCustom    Preset = "custom"
)

// Mode says whether the bot acts in a group (DESIGN.md §8.4).
type Mode string

// Modes. Shadow records what would have happened and acts on nothing; paused does nothing.
const (
	ModeShadow  Mode = "shadow"
	ModeEnforce Mode = "enforce"
	ModePaused  Mode = "paused"
)

// EnforcementMode says who decides bans (DESIGN.md §8.3). Both ask modes never auto-ban.
type EnforcementMode string

// Enforcement modes.
const (
	EnforceAuto            EnforcementMode = "auto"
	EnforceAskDeleteFirst  EnforcementMode = "ask_delete_first"
	EnforceAskBeforeAction EnforcementMode = "ask_before_action"
)

// AsksAdmins reports whether m is one of the always-ask-admins modes.
func (m EnforcementMode) AsksAdmins() bool {
	return m == EnforceAskDeleteFirst || m == EnforceAskBeforeAction
}

// ViolationAction is what happens to a content-rule violation (DESIGN.md §9.3).
type ViolationAction string

// Violation actions. The default is a silent delete with no ban and no strike.
const (
	ViolationDeleteOnly ViolationAction = "delete_only"
	ViolationAsk        ViolationAction = "ask"
	ViolationBan        ViolationAction = "ban"
)

// TimeoutAction is what happens when admins don't answer a review in time (DESIGN.md §9.5).
type TimeoutAction string

// Timeout actions. Leave is only for ask_before_action: the message stays and nothing happens.
const (
	TimeoutKeepDeletedNoBan TimeoutAction = "keep_deleted_no_ban"
	TimeoutDeleteNoBan      TimeoutAction = "delete_no_ban"
	TimeoutAutoBan          TimeoutAction = "auto_ban"
	TimeoutDismiss          TimeoutAction = "dismiss"
	TimeoutLeave            TimeoutAction = "leave"
)

// ContentRule is a per-type content policy (DESIGN.md §9.3). Everything but allow is opt-in.
type ContentRule string

// Content rules.
const (
	RuleAllow             ContentRule = "allow"
	RuleBlock             ContentRule = "block"
	RuleBlockForNew       ContentRule = "block_for_new"
	RuleBlockForUntrusted ContentRule = "block_for_untrusted"
)

// Duration is a time.Duration written as a Go duration string ("24h") in the settings document.
type Duration time.Duration

// MarshalJSON writes d as a duration string.
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

// UnmarshalJSON reads a duration string such as "15m" or "168h".
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"24h\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// Settings is one group's settings document (DESIGN.md §12.1). Every leaf has a dotted key
// built from the JSON names (enforcement.autoban_matrix.videos.action, review.timeout, ...).
type Settings struct {
	Preset        Preset          `json:"preset"`
	Mode          Mode            `json:"mode"`
	Silent        bool            `json:"silent"`
	Shadow        Shadow          `json:"shadow"`
	Enforcement   Enforcement     `json:"enforcement"`
	Uncertain     Uncertain       `json:"uncertain"`
	Policy        ViolationPolicy `json:"policy"`
	Review        Review          `json:"review"`
	Content       Content         `json:"content"`
	Detection     Detection       `json:"detection"`
	NewMembers    NewMembers      `json:"new_members"`
	Ladder        Ladder          `json:"ladder"`
	Mute          Mute            `json:"mute"`
	Appeals       Appeals         `json:"appeals"`
	Compromised   Compromised     `json:"compromised"`
	Raid          Raid            `json:"raid"`
	Allowlist     Allowlist       `json:"allowlist"`
	BlocklistRefs []string        `json:"blocklist_refs"`
	Learning      Learning        `json:"learning"`
	Network       Network         `json:"network"`
	Intel         Intel           `json:"intel"`
	Limits        Limits          `json:"limits"`
}

// Shadow configures what shadow mode shows in the group (DESIGN.md §8.4).
type Shadow struct {
	// Reactions: react to each message shadow mode caught, even while the group is silent: ❌
	// when the bot would act without asking, ❓ when it would ask the admins. Nothing else is
	// posted, deleted or banned.
	Reactions bool `json:"reactions"`
}

// Enforcement decides who bans and when (DESIGN.md §8.3, §9.2).
type Enforcement struct {
	Mode          EnforcementMode      `json:"mode"`
	AskBefore     AskBefore            `json:"ask_before"`
	Established   Established          `json:"established"`
	Ban           Ban                  `json:"ban"`
	AutobanMatrix map[ContentType]Cell `json:"autoban_matrix"`
}

// AskBefore is the timeout fallback of ask_before_action: by default the message is deleted
// after 15 minutes without a ban; leave keeps it. The fallback can never ban (DESIGN.md §8.3).
type AskBefore struct {
	Timeout   Duration      `json:"timeout"`
	OnTimeout TimeoutAction `json:"on_timeout"`
}

// Established is the bar above which a member is never auto-banned (DESIGN.md §8.1).
type Established struct {
	MinDays          int `json:"min_days"`
	MinCleanMessages int `json:"min_clean_messages"`
}

// Ban configures what a ban does (DESIGN.md §9.1).
type Ban struct {
	RejoinBlock bool `json:"rejoin_block"`
}

// Uncertain configures the still-uncertain band (DESIGN.md §7.0).
type Uncertain struct {
	DeleteWhilePending bool `json:"delete_while_pending"`
}

// ViolationPolicy is the reaction to content-rule violations (DESIGN.md §9.3).
type ViolationPolicy struct {
	Action ViolationAction `json:"action"`
	Repeat *Repeat         `json:"repeat"`
}

// Repeat escalates repeated violations: Count violations within Window lead to Then.
type Repeat struct {
	Count  int             `json:"count"`
	Window Duration        `json:"window"`
	Then   ViolationAction `json:"then"`
}

// Review configures admin reviews (DESIGN.md §9.5).
type Review struct {
	Destination string        `json:"destination" enum:"dm,group,dashboard"`
	Reviewers   []string      `json:"reviewers"`
	ReviewGroup string        `json:"review_group"`
	Send        ReviewSend    `json:"send"`
	Timeout     Duration      `json:"timeout"`
	OnTimeout   TimeoutAction `json:"on_timeout"`
	Reactions   Reactions     `json:"reactions"`
	QuietHours  string        `json:"quiet_hours"`
	Language    string        `json:"language"`
	BlurMedia   bool          `json:"blur_media"`
}

// ReviewSend picks which kinds of items go to the review destination.
type ReviewSend struct {
	Uncertain   bool `json:"uncertain"`
	Appeals     bool `json:"appeals"`
	Alerts      bool `json:"alerts"`
	DailyDigest bool `json:"daily_digest"`
}

// Reactions are the emoji that decide a review.
type Reactions struct {
	Ban     []string `json:"ban"`
	NotSpam []string `json:"not_spam"`
}

// Content is the per-type content policy (DESIGN.md §9.3).
type Content struct {
	Links                   ContentRule `json:"links"`
	LinkDomains             DomainLists `json:"link_domains"`
	GroupInvites            ContentRule `json:"group_invites"`
	ChannelShares           ContentRule `json:"channel_shares"`
	Contacts                ContentRule `json:"contacts"`
	Images                  ContentRule `json:"images"`
	Videos                  ContentRule `json:"videos"`
	Stickers                ContentRule `json:"stickers"`
	Documents               ContentRule `json:"documents"`
	Audio                   ContentRule `json:"audio"`
	Polls                   ContentRule `json:"polls"`
	Locations               ContentRule `json:"locations"`
	Events                  ContentRule `json:"events"`
	ViewOnce                ContentRule `json:"view_once"`
	ForwardedManyTimes      ContentRule `json:"forwarded_many_times"`
	StatusShares            ContentRule `json:"status_shares"`
	StatusMentions          ContentRule `json:"status_mentions"`
	GroupStatusPosts        ContentRule `json:"group_status_posts"`
	DocumentExtensionsBlock []string    `json:"document_extensions_block"`
}

// DomainLists are per-group link domain lists: allow exempts a domain from a links rule, block
// makes links to it a violation even when links are allowed.
type DomainLists struct {
	Allow []string `json:"allow"`
	Block []string `json:"block"`
}

// Detection holds the per-group detection settings (DESIGN.md §7.3, §7.4).
type Detection struct {
	Thresholds          Thresholds  `json:"thresholds"`
	AI                  AISettings  `json:"ai"`
	Certain             CertainBand `json:"certain"`
	NetworkIntelligence bool        `json:"network_intelligence"`
	Languages           []string    `json:"languages"`
}

// Thresholds are the score lines (DESIGN.md §7.3).
type Thresholds struct {
	Review float64 `json:"review"`
	Delete float64 `json:"delete"`
	Ban    float64 `json:"ban"`
}

// AISettings configures AI verification for the group (DESIGN.md §7.4).
type AISettings struct {
	Enabled           bool    `json:"enabled"`
	Vision            bool    `json:"vision"`
	DailyBudgetCalls  int     `json:"daily_budget_calls"`
	SpamConfidence    float64 `json:"spam_confidence"`
	NotSpamConfidence float64 `json:"not_spam_confidence"`
}

// CertainBand holds the tight CERTAIN thresholds (operator-only, DESIGN.md §7.0).
type CertainBand struct {
	SimhashMaxDistance int     `json:"simhash_max_distance"`
	MinhashMinJaccard  float64 `json:"minhash_min_jaccard"`
	PhashMaxDistance   int     `json:"phash_max_distance"`
}

// NewMembers configures how new members are treated (DESIGN.md §8.2).
type NewMembers struct {
	JustJoined            JustJoined `json:"just_joined"`
	ScreenOnJoin          bool       `json:"screen_on_join"`
	JoinApprovalAutopilot bool       `json:"join_approval_autopilot"`
}

// JustJoined is the invisible just-joined scoring signal (DESIGN.md §8.2).
type JustJoined struct {
	Enabled bool     `json:"enabled"`
	Weight  float64  `json:"weight"`
	Window  Duration `json:"window"`
}

// Ladder is the optional warn → mute → ban ladder (DESIGN.md §9.3), off by default.
type Ladder struct {
	Enabled bool         `json:"enabled"`
	Window  Duration     `json:"window"`
	Steps   []LadderStep `json:"steps"`
	WarnVia string       `json:"warn_via"`
}

// LadderStep is one rung of the ladder: at the At-th strike, do Do.
type LadderStep struct {
	At int      `json:"at"`
	Do []string `json:"do"`
}

// Mute configures mute emulation (only with the ladder).
type Mute struct {
	FloodEscalateToBan bool `json:"flood_escalate_to_ban"`
}

// Appeals configures appeal notices (DESIGN.md §8.5).
type Appeals struct {
	NotifyRemoved bool `json:"notify_removed"`
}

// Compromised configures the possibly-compromised response (DESIGN.md §8.7).
type Compromised struct {
	Enabled              bool     `json:"enabled"`
	MinSignals           int      `json:"min_signals"`
	RestrictWhilePending bool     `json:"restrict_while_pending"`
	Restrict             []string `json:"restrict"`
	Scope                string   `json:"scope" enum:"all_groups,this_group"`
	Admins               string   `json:"admins" enum:"alert_only,delete_known_spam"`
}

// Raid configures raid mode (DESIGN.md §9.4).
type Raid struct {
	Enabled                    bool `json:"enabled"`
	AutoLockdownMinutes        int  `json:"auto_lockdown_minutes"`
	LockdownWhenBacklogSeconds int  `json:"lockdown_when_backlog_seconds"`
	ReopenWhenVisibleClean     bool `json:"reopen_when_visible_clean"`
}

// Allowlist holds the group's allow-listed members, domains and fingerprints.
type Allowlist struct {
	Members      []string `json:"members"`
	Domains      []string `json:"domains"`
	Fingerprints []string `json:"fingerprints"`
}

// Learning configures learning from confirmed spam (DESIGN.md §7.5).
type Learning struct {
	Enabled           bool    `json:"enabled"`
	AutoLearnMinScore float64 `json:"auto_learn_min_score"`
	ShareToGlobal     bool    `json:"share_to_global"`
}

// Network configures the shared ban list (DESIGN.md §9.6).
type Network struct {
	Contribute            bool   `json:"contribute"`
	Enforce               string `json:"enforce" enum:"off,org,all"`
	CleanupDeleteMessages bool   `json:"cleanup_delete_messages"`
	OutOfScopeBans        string `json:"out_of_scope_bans" enum:"risk_signal,ignore"`
	SendInviteOnUnban     bool   `json:"send_invite_on_unban"`
}

// Intel configures identity priors (DESIGN.md §7.6); they never act on their own.
type Intel struct {
	UsePriors           bool `json:"use_priors"`
	NumberPrefixSignal  bool `json:"number_prefix_signal"`
	PictureAndNameReuse bool `json:"picture_and_name_reuse"`
}

// Limits are the per-group safety rails (DESIGN.md §8.6).
type Limits struct {
	MaxBansPerHour        int `json:"max_bans_per_hour"`
	MaxDeletionsPerMinute int `json:"max_deletions_per_minute"`
}

// Defaults returns the default settings: the Standard preset, enforcing, silent.
func Defaults() Settings {
	s := Settings{
		Preset: PresetStandard,
		Mode:   ModeEnforce,
		Silent: true,
		Enforcement: Enforcement{
			Mode:        EnforceAuto,
			AskBefore:   AskBefore{Timeout: Duration(15 * time.Minute), OnTimeout: TimeoutDeleteNoBan},
			Established: Established{MinDays: 7, MinCleanMessages: 20},
			Ban:         Ban{RejoinBlock: true},
		},
		Policy: ViolationPolicy{Action: ViolationDeleteOnly},
		Review: Review{
			Destination: "dashboard",
			Reviewers:   []string{},
			Send:        ReviewSend{Uncertain: true, Appeals: true, Alerts: true},
			Timeout:     Duration(6 * time.Hour),
			OnTimeout:   TimeoutKeepDeletedNoBan,
			Reactions:   Reactions{Ban: []string{"✅", "👍"}, NotSpam: []string{"❌", "👎"}},
			QuietHours:  "00:00-07:00",
			Language:    "en",
		},
		Content: Content{
			Links: RuleAllow, LinkDomains: DomainLists{Allow: []string{}, Block: []string{}},
			GroupInvites: RuleAllow, ChannelShares: RuleAllow, Contacts: RuleAllow,
			Images: RuleAllow, Videos: RuleAllow, Stickers: RuleAllow, Documents: RuleAllow,
			Audio: RuleAllow, Polls: RuleAllow, Locations: RuleAllow, Events: RuleAllow,
			ViewOnce: RuleAllow, ForwardedManyTimes: RuleAllow,
			StatusShares: RuleAllow, StatusMentions: RuleAllow, GroupStatusPosts: RuleAllow,
			DocumentExtensionsBlock: []string{"apk", "exe", "scr", "bat", "js"},
		},
		Detection: Detection{
			Thresholds:          Thresholds{Review: 0.60, Delete: 0.85, Ban: 0.95},
			AI:                  AISettings{Enabled: true, DailyBudgetCalls: 200, SpamConfidence: 0.90, NotSpamConfidence: 0.90},
			Certain:             CertainBand{SimhashMaxDistance: 3, MinhashMinJaccard: 0.9, PhashMaxDistance: 4},
			NetworkIntelligence: true,
			Languages:           []string{"ar", "en"},
		},
		NewMembers: NewMembers{
			JustJoined:   JustJoined{Enabled: true, Weight: 0.8, Window: Duration(24 * time.Hour)},
			ScreenOnJoin: true,
		},
		Ladder: Ladder{
			Window: Duration(168 * time.Hour),
			Steps: []LadderStep{
				{At: 1, Do: []string{"delete", "warn"}},
				{At: 2, Do: []string{"delete", "mute:1h"}},
				{At: 3, Do: []string{"delete", "ban"}},
			},
			WarnVia: "none",
		},
		Mute: Mute{FloodEscalateToBan: true},
		Compromised: Compromised{
			Enabled: true, MinSignals: 2,
			Restrict: []string{"links", "media", "forwards"},
			Scope:    "all_groups", Admins: "alert_only",
		},
		Raid:          Raid{Enabled: true, AutoLockdownMinutes: 15, LockdownWhenBacklogSeconds: 60, ReopenWhenVisibleClean: true},
		Allowlist:     Allowlist{Members: []string{}, Domains: []string{}, Fingerprints: []string{}},
		BlocklistRefs: []string{},
		Learning:      Learning{Enabled: true, AutoLearnMinScore: 0.98, ShareToGlobal: true},
		Network:       Network{Contribute: true, Enforce: "org", CleanupDeleteMessages: true, OutOfScopeBans: "risk_signal", SendInviteOnUnban: true},
		Intel:         Intel{UsePriors: true, NumberPrefixSignal: true, PictureAndNameReuse: true},
		Limits:        Limits{MaxBansPerHour: 10, MaxDeletionsPerMinute: 30},
	}
	applyPreset(&s, PresetStandard)
	return s
}

// Clone returns a deep copy of s, so changing the copy never changes s.
func (s Settings) Clone() Settings {
	c := s
	c.Enforcement.AutobanMatrix = make(map[ContentType]Cell, len(s.Enforcement.AutobanMatrix))
	for k, v := range s.Enforcement.AutobanMatrix {
		v.RequireSource = slices.Clone(v.RequireSource)
		c.Enforcement.AutobanMatrix[k] = v
	}
	if s.Policy.Repeat != nil {
		r := *s.Policy.Repeat
		c.Policy.Repeat = &r
	}
	c.Review.Reviewers = slices.Clone(s.Review.Reviewers)
	c.Review.Reactions.Ban = slices.Clone(s.Review.Reactions.Ban)
	c.Review.Reactions.NotSpam = slices.Clone(s.Review.Reactions.NotSpam)
	c.Content.LinkDomains.Allow = slices.Clone(s.Content.LinkDomains.Allow)
	c.Content.LinkDomains.Block = slices.Clone(s.Content.LinkDomains.Block)
	c.Content.DocumentExtensionsBlock = slices.Clone(s.Content.DocumentExtensionsBlock)
	c.Detection.Languages = slices.Clone(s.Detection.Languages)
	c.Ladder.Steps = make([]LadderStep, len(s.Ladder.Steps))
	for i, st := range s.Ladder.Steps {
		c.Ladder.Steps[i] = LadderStep{At: st.At, Do: slices.Clone(st.Do)}
	}
	c.Compromised.Restrict = slices.Clone(s.Compromised.Restrict)
	c.Allowlist.Members = slices.Clone(s.Allowlist.Members)
	c.Allowlist.Domains = slices.Clone(s.Allowlist.Domains)
	c.Allowlist.Fingerprints = slices.Clone(s.Allowlist.Fingerprints)
	c.BlocklistRefs = slices.Clone(s.BlocklistRefs)
	return c
}

// ErrInvalidSettings wraps every validation failure.
var ErrInvalidSettings = errors.New("invalid settings")

func invalid(key, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalidSettings, key, fmt.Sprintf(format, args...))
}

func oneOf[T ~string](key string, v T, allowed ...T) error {
	if slices.Contains(allowed, v) {
		return nil
	}
	names := make([]string, len(allowed))
	for i, a := range allowed {
		names[i] = string(a)
	}
	return invalid(key, "%q is not one of %s", v, strings.Join(names, ", "))
}

func unit(key string, v float64) error {
	if v < 0 || v > 1 {
		return invalid(key, "%v is outside 0..1", v)
	}
	return nil
}

// Validate reports every problem with s, joined. A document that fails validation is never
// stored, so routing can rely on the values being in range.
func (s Settings) Validate() error {
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	add(oneOf("preset", s.Preset, PresetRelaxed, PresetStandard, PresetStrict, PresetAskAdmins, PresetCustom))
	add(oneOf("mode", s.Mode, ModeShadow, ModeEnforce, ModePaused))

	e := s.Enforcement
	add(oneOf("enforcement.mode", e.Mode, EnforceAuto, EnforceAskDeleteFirst, EnforceAskBeforeAction))
	if e.AskBefore.Timeout <= 0 {
		add(invalid("enforcement.ask_before.timeout", "must be positive"))
	}
	add(oneOf("enforcement.ask_before.on_timeout", e.AskBefore.OnTimeout, TimeoutDeleteNoBan, TimeoutLeave))
	if e.Established.MinDays < 0 {
		add(invalid("enforcement.established.min_days", "must not be negative"))
	}
	if e.Established.MinCleanMessages < 0 {
		add(invalid("enforcement.established.min_clean_messages", "must not be negative"))
	}
	for _, t := range ContentTypes() {
		c, ok := e.AutobanMatrix[t]
		key := "enforcement.autoban_matrix." + string(t)
		if !ok {
			add(invalid(key, "missing"))
			continue
		}
		add(c.validate(key))
	}
	for _, t := range slices.Sorted(maps.Keys(e.AutobanMatrix)) {
		if !t.valid() {
			add(invalid("enforcement.autoban_matrix."+string(t), "unknown content type"))
		}
	}

	add(oneOf("policy.action", s.Policy.Action, ViolationDeleteOnly, ViolationAsk, ViolationBan))
	if r := s.Policy.Repeat; r != nil {
		if r.Count < 1 {
			add(invalid("policy.repeat.count", "must be at least 1"))
		}
		if r.Window <= 0 {
			add(invalid("policy.repeat.window", "must be positive"))
		}
		add(oneOf("policy.repeat.then", r.Then, ViolationAsk, ViolationBan))
	}

	add(oneOf("review.destination", s.Review.Destination, "dm", "group", "dashboard"))
	if s.Review.Timeout <= 0 {
		add(invalid("review.timeout", "must be positive"))
	}
	add(oneOf("review.on_timeout", s.Review.OnTimeout, TimeoutKeepDeletedNoBan, TimeoutDeleteNoBan, TimeoutAutoBan, TimeoutDismiss))
	if s.Review.OnTimeout == TimeoutAutoBan && e.Mode.AsksAdmins() {
		add(invalid("review.on_timeout", "auto_ban is not allowed while enforcement.mode is %s (ask modes never ban)", e.Mode))
	}

	c := s.Content
	for _, r := range []struct {
		key  string
		rule ContentRule
	}{
		{"content.links", c.Links},
		{"content.group_invites", c.GroupInvites},
		{"content.channel_shares", c.ChannelShares},
		{"content.contacts", c.Contacts},
		{"content.images", c.Images},
		{"content.videos", c.Videos},
		{"content.stickers", c.Stickers},
		{"content.documents", c.Documents},
		{"content.audio", c.Audio},
		{"content.polls", c.Polls},
		{"content.locations", c.Locations},
		{"content.events", c.Events},
		{"content.view_once", c.ViewOnce},
		{"content.forwarded_many_times", c.ForwardedManyTimes},
		{"content.status_shares", c.StatusShares},
		{"content.status_mentions", c.StatusMentions},
		{"content.group_status_posts", c.GroupStatusPosts},
	} {
		add(oneOf(r.key, r.rule, RuleAllow, RuleBlock, RuleBlockForNew, RuleBlockForUntrusted))
	}

	th := s.Detection.Thresholds
	add(unit("detection.thresholds.review", th.Review))
	add(unit("detection.thresholds.delete", th.Delete))
	add(unit("detection.thresholds.ban", th.Ban))
	if th.Review > th.Delete || th.Delete > th.Ban {
		add(invalid("detection.thresholds", "must satisfy review ≤ delete ≤ ban (got %v, %v, %v)", th.Review, th.Delete, th.Ban))
	}
	add(unit("detection.ai.spam_confidence", s.Detection.AI.SpamConfidence))
	add(unit("detection.ai.not_spam_confidence", s.Detection.AI.NotSpamConfidence))
	if s.Detection.AI.DailyBudgetCalls < 0 {
		add(invalid("detection.ai.daily_budget_calls", "must not be negative"))
	}

	jj := s.NewMembers.JustJoined
	if jj.Weight < 0 {
		add(invalid("new_members.just_joined.weight", "must not be negative"))
	}
	if jj.Window <= 0 {
		add(invalid("new_members.just_joined.window", "must be positive"))
	}
	add(oneOf("compromised.admins", s.Compromised.Admins, "alert_only", "delete_known_spam"))
	add(oneOf("compromised.scope", s.Compromised.Scope, "all_groups", "this_group"))
	add(oneOf("network.enforce", s.Network.Enforce, "off", "org", "all"))
	add(oneOf("network.out_of_scope_bans", s.Network.OutOfScopeBans, "risk_signal", "ignore"))
	if s.Limits.MaxBansPerHour < 0 {
		add(invalid("limits.max_bans_per_hour", "must not be negative"))
	}
	if s.Limits.MaxDeletionsPerMinute < 1 {
		add(invalid("limits.max_deletions_per_minute", "must be at least 1"))
	}
	return errors.Join(errs...)
}
