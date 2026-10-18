// Package blacklist is the manual phone-number blacklist (DESIGN.md §9.8): adding, removing,
// listing and checking numbers, resolving them to LIDs within the read budget (§7.7), and
// enforcing entries silently through the paced action queue.
package blacklist

import (
	"context"
	"errors"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Scope is where an entry applies.
type Scope string

// Scopes.
const (
	ScopeGroup    Scope = "group"
	ScopeInstance Scope = "instance" // operator only; groups use it unless blacklist.use_instance is false
)

// Errors.
var (
	ErrNotOperator = errors.New("blacklist: only the operator can change instance-wide entries")
	ErrInvalid     = errors.New("blacklist: invalid phone number")
)

// Entry is one active blacklist entry (§14 number_blacklist).
type Entry struct {
	ID         int64
	Scope      Scope
	Group      wa.JID // empty for instance entries
	E164       string // "+9665…"
	Person     wa.JID // the LID once resolved
	OnWhatsApp *bool  // nil until resolved
	Reason     string
	AddedBy    wa.JID
	AddedAt    time.Time
}

// PhoneJID is the entry's phone-number JID.
func (e Entry) PhoneJID() wa.JID { return phoneJID(e.E164) }

// List stores entries.
type List interface {
	// Add stores e; added is false when the number is already listed in that scope and group.
	Add(ctx context.Context, e Entry) (stored Entry, added bool, err error)
	// Remove removes the number from that scope and group; removed is false if it wasn't listed.
	Remove(ctx context.Context, scope Scope, group wa.JID, e164 string, by wa.JID) (removed bool, err error)
	// ForGroup lists a group's own entries, oldest first.
	ForGroup(ctx context.Context, group wa.JID) ([]Entry, error)
	// Instance lists the instance-wide entries.
	Instance(ctx context.Context) ([]Entry, error)
	// ByNumber returns every active entry for a number, in any scope.
	ByNumber(ctx context.Context, e164 string) ([]Entry, error)
	// ByPerson returns every active entry resolved to a LID.
	ByPerson(ctx context.Context, lid wa.JID) ([]Entry, error)
	// SetResolved records what resolution found for a number.
	SetResolved(ctx context.Context, e164 string, lid wa.JID, onWhatsApp bool) error
}

// Normalizer turns a number in any common format into E.164 (F-123): international, local with
// the group's default country, 00-prefixed, Arabic-Indic digits and wa.me links.
type Normalizer interface {
	Normalize(input, defaultCountry string) (e164 string, err error)
}

// Member is a protected group's participant as enforcement needs it.
type Member struct {
	JID   wa.JID // LID or phone JID, whichever the group uses
	LID   wa.JID
	Phone wa.JID
	Admin bool
}

// Groups answers questions about protected groups.
type Groups interface {
	// Protected lists every group the bot protects.
	Protected(ctx context.Context) ([]wa.JID, error)
	Members(ctx context.Context, group wa.JID) ([]Member, error)
}

// GroupSettings is the part of a group's settings the blacklist needs.
type GroupSettings struct {
	UseInstance bool // blacklist.use_instance (default true)
	// Inactive is set for groups in shadow or paused mode: the blacklist acts on nothing there
	// (no bans, kicks, rejections, deletions or reviews), like the rest of moderation (§8.4).
	Inactive       bool
	Mode           review.EnforcementMode
	DefaultCountry string // ISO region for local numbers, e.g. "SA"
}

// Settings returns a group's blacklist settings.
type Settings interface {
	Blacklist(ctx context.Context, group wa.JID) (GroupSettings, error)
}

// Reviews opens review items (implemented by *review.Service).
type Reviews interface {
	Open(ctx context.Context, n review.NewItem) (review.Item, error)
}

// Notifier tells an admin something by DM when they have an open thread (or through the
// review destination); it never posts in a protected group.
type Notifier interface {
	Notify(ctx context.Context, to, group wa.JID, key string, vars map[string]string) error
}

// Deps are the Service's collaborators.
type Deps struct {
	List      List
	Normalize Normalizer
	Resolver  *Resolver
	Groups    Groups
	Settings  Settings
	Actions   review.Actions
	Reviews   Reviews
	Notifier  Notifier
	Audit     review.Audit
	Clock     clock.Clock
}

// Service manages and enforces the blacklist.
type Service struct {
	d    Deps
	memo reviewMemo
}

// NewService returns a Service.
func NewService(d Deps) *Service {
	if d.Clock == nil {
		d.Clock = clock.Real{}
	}
	if d.Normalize == nil {
		d.Normalize = SimpleNormalizer{}
	}
	return &Service{d: d}
}

// AddRequest adds numbers to a group's list, or instance-wide when Instance is set (operator only).
type AddRequest struct {
	Group    wa.JID
	Numbers  []string // raw input, any common format
	Reason   string
	By       wa.JID
	Operator bool // the requester is the instance operator
	Instance bool
}

// AddResult reports what happened to each number.
type AddResult struct {
	Added   []Entry
	Already []string // E.164 numbers already listed
	Invalid []string // raw inputs that aren't valid numbers
	Banned  []wa.JID // members banned as a result
	Skipped []wa.JID // admins who were listed but never acted on; the adder is told
	Review  []string // short codes of review items opened instead (ask-admins groups)
}

// Add normalizes and stores numbers, resolves them, and bans current members silently.
func (s *Service) Add(ctx context.Context, r AddRequest) (AddResult, error) {
	return s.add(ctx, r)
}

// RemoveRequest removes numbers from a group's list, or instance-wide when Instance is set.
type RemoveRequest struct {
	Group    wa.JID
	Numbers  []string
	By       wa.JID
	Operator bool
	Instance bool
}

// RemoveResult reports what happened to each number.
type RemoveResult struct {
	Removed  []string
	NotFound []string
	Invalid  []string
}

// Remove removes numbers and lifts their rejoin blocks; it never re-adds anyone.
func (s *Service) Remove(ctx context.Context, r RemoveRequest) (RemoveResult, error) {
	return s.remove(ctx, r)
}

// ForGroup lists a group's own entries.
func (s *Service) ForGroup(ctx context.Context, group wa.JID) ([]Entry, error) {
	return s.d.List.ForGroup(ctx, group)
}

// CheckResult says whether a number is listed and whether its owner is a member.
type CheckResult struct {
	E164     string
	Group    *Entry // the group's own entry, if any
	Instance *Entry // the instance-wide entry, if any (only when the group uses it)
	Member   bool
}

// Check reports a number's status in a group.
func (s *Service) Check(ctx context.Context, group wa.JID, number string) (CheckResult, error) {
	return s.check(ctx, group, number)
}

// Person identifies someone seen in a group: their LID and phone JID when known.
type Person struct {
	JID   wa.JID
	LID   wa.JID
	Phone wa.JID
}

// OnJoin kicks a blacklisted joiner (a ban, so the rejoin block applies). It reports whether
// the person was blacklisted for the group.
func (s *Service) OnJoin(ctx context.Context, group wa.JID, p Person) (bool, error) {
	return s.onJoin(ctx, group, p)
}

// OnJoinRequests rejects pending join requests from blacklisted people in one batch.
func (s *Service) OnJoinRequests(ctx context.Context, group wa.JID, people []Person) ([]wa.JID, error) {
	return s.onJoinRequests(ctx, group, people)
}

// OnMessage deletes a blacklisted sender's message at P1, and bans them if they're still a
// member and not an admin. It reports whether the sender was blacklisted.
func (s *Service) OnMessage(ctx context.Context, group wa.JID, msg wa.MessageRef, sender Person) (bool, error) {
	return s.onMessage(ctx, group, msg, sender)
}

// Listed returns the entry that applies to a person in a group, if any.
func (s *Service) Listed(ctx context.Context, group wa.JID, p Person) (Entry, bool, error) {
	return s.listed(ctx, group, p)
}
