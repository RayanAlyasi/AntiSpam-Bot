package blacklist

import (
	"context"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// DefaultBatch is how many numbers one GetUserInfo call looks up.
const DefaultBatch = 50

// UserLookup is the read the resolver makes; wa.WAClient implements it.
type UserLookup interface {
	UserInfo(ctx context.Context, jids []wa.JID) (map[wa.JID]wa.UserInfo, error)
}

// ReadBudget paces reads (IQs) in their own bucket, separate from writes (§10.3, F-070).
// Wait blocks until one read may be made.
type ReadBudget interface {
	Wait(ctx context.Context) error
}

// Linker links a phone-number identity and a LID to the same person (§7.7, F-022).
type Linker interface {
	Link(ctx context.Context, phone, lid wa.JID) error
}

// Resolution is what resolving one number found.
type Resolution struct {
	E164       string
	Phone      wa.JID
	LID        wa.JID // empty when not on WhatsApp
	OnWhatsApp bool
}

// ResolverDeps are the Resolver's collaborators.
type ResolverDeps struct {
	Users  UserLookup
	Budget ReadBudget
	Linker Linker
	List   List // resolutions are written back with SetResolved
	Batch  int  // defaults to DefaultBatch
}

// Resolver resolves blacklisted numbers to LIDs with batched GetUserInfo calls inside the read
// budget (F-125). A number that isn't on WhatsApp still works as a content signal.
type Resolver struct {
	d ResolverDeps
}

// NewResolver returns a Resolver.
func NewResolver(d ResolverDeps) *Resolver { return &Resolver{d: d} }

// Resolve looks up numbers (E.164) in batches, waiting on the read budget before each call,
// links each found LID to its phone identity and records the result on the entries.
func (r *Resolver) Resolve(ctx context.Context, numbers []string) ([]Resolution, error) {
	return r.resolve(ctx, numbers)
}

// SimpleNormalizer is a dependency-free Normalizer for tests and for running before the
// libphonenumber-backed one (F-123) is wired: it accepts +, 00 and wa.me forms, Arabic-Indic
// and Persian digits, separators, and local numbers for a few default countries (SA, AE, EG,
// KW, QA, BH, OM, JO, GB, US).
type SimpleNormalizer struct{}

var _ Normalizer = SimpleNormalizer{}

// Normalize returns the E.164 form of input, or an error wrapping ErrInvalid.
func (SimpleNormalizer) Normalize(input, defaultCountry string) (string, error) {
	return normalizeNumber(input, defaultCountry)
}
