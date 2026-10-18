package pgstore

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	dblacklist "github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/corpus"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/fingerprint"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/textnorm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// PersonMap maps JIDs to persons and back (identity, F-020). queue.IdentityPersons implements it.
type PersonMap interface {
	PersonID(ctx context.Context, jid wa.JID) (int64, error)
	// JID returns the person's LID, or their phone JID when no LID is known.
	JID(ctx context.Context, id int64) (wa.JID, error)
}

// PersonMatcher finds a person without creating one; read paths prefer it when PersonMap has it.
type PersonMatcher interface {
	MatchPersonID(ctx context.Context, jid wa.JID) (int64, bool, error)
}

// jidOf returns a person's JID; a person who no longer exists (merged away) is just unknown,
// so one stale row never fails a whole listing.
func jidOf(ctx context.Context, p PersonMap, id int64) (wa.JID, error) {
	j, err := p.JID(ctx, id)
	if errors.Is(err, identity.ErrNoPerson) {
		return "", nil
	}
	return j, err
}

func optPerson(ctx context.Context, p PersonMap, jid wa.JID) (*int64, error) {
	if jid == "" || p == nil {
		return nil, nil
	}
	id, err := p.PersonID(ctx, jid)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func nowOf(c clock.Clock) time.Time {
	if c == nil {
		return clock.Real{}.Now()
	}
	return c.Now()
}

// BlacklistList is review/blacklist's List over WP-G's number_blacklist (F-123), so content
// matching (F-127) sees every entry the commands add.
type BlacklistList struct {
	DB db.DBTX
	// HashKey is the deployment's number-hash key (at least 16 bytes), shared with content
	// matching, so every entry's pn_hash is one the analyzer looks for.
	HashKey []byte
	Persons PersonMap
	Clock   clock.Clock
}

var _ blacklist.List = BlacklistList{}

type blRow struct {
	ID            int64
	Scope         string
	GroupJid      pgtype.Text
	PnE164        string
	PersonID      pgtype.Int8
	OnWhatsapp    pgtype.Bool
	Reason        string
	AddedByPerson pgtype.Int8
	AddedAt       time.Time
}

func (l BlacklistList) entry(ctx context.Context, r blRow) (blacklist.Entry, error) {
	e := blacklist.Entry{
		ID: r.ID, Scope: blacklist.Scope(r.Scope), Group: wa.JID(r.GroupJid.String), E164: r.PnE164,
		Reason: r.Reason, AddedAt: r.AddedAt,
	}
	if r.OnWhatsapp.Valid {
		on := r.OnWhatsapp.Bool
		e.OnWhatsApp = &on
	}
	if l.Persons == nil {
		return e, nil
	}
	if r.PersonID.Valid {
		j, err := jidOf(ctx, l.Persons, r.PersonID.Int64)
		if err != nil {
			return e, fmt.Errorf("person %d: %w", r.PersonID.Int64, err)
		}
		if j.IsLID() {
			e.Person = j
		}
	}
	if r.AddedByPerson.Valid {
		j, err := jidOf(ctx, l.Persons, r.AddedByPerson.Int64)
		if err != nil {
			return e, fmt.Errorf("person %d: %w", r.AddedByPerson.Int64, err)
		}
		e.AddedBy = j
	}
	return e, nil
}

func (l BlacklistList) entries(ctx context.Context, rows []blRow) ([]blacklist.Entry, error) {
	out := make([]blacklist.Entry, 0, len(rows))
	for _, r := range rows {
		e, err := l.entry(ctx, r)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func fromStore(e store.BlacklistEntry) blRow {
	r := blRow{ID: e.ID, Scope: string(e.Scope), GroupJid: text(e.GroupJID), PnE164: e.E164, Reason: e.Reason, AddedAt: e.AddedAt}
	if e.PersonID != nil {
		r.PersonID = pgtype.Int8{Int64: *e.PersonID, Valid: true}
	}
	if e.OnWhatsApp != nil {
		r.OnWhatsapp = pgtype.Bool{Bool: *e.OnWhatsApp, Valid: true}
	}
	if e.AddedBy != nil {
		r.AddedByPerson = pgtype.Int8{Int64: *e.AddedBy, Valid: true}
	}
	return r
}

// Add stores an entry with its keyed hash.
func (l BlacklistList) Add(ctx context.Context, e blacklist.Entry) (blacklist.Entry, bool, error) {
	by, err := optPerson(ctx, l.Persons, e.AddedBy)
	if err != nil {
		return blacklist.Entry{}, false, fmt.Errorf("adder: %w", err)
	}
	at := e.AddedAt
	if at.IsZero() {
		at = nowOf(l.Clock)
	}
	hasher, err := dblacklist.NewHasher(l.HashKey)
	if err != nil {
		return blacklist.Entry{}, false, fmt.Errorf("number hash key: %w", err)
	}
	got, added, err := store.AddBlacklist(ctx, l.DB, store.BlacklistAdd{
		Scope: rules.BlacklistScope(e.Scope), GroupJID: string(e.Group), E164: e.E164, Hash: hasher.Hash(e.E164),
		Reason: e.Reason, AddedBy: by, Now: at,
	})
	if err != nil {
		return blacklist.Entry{}, false, err
	}
	out, err := l.entry(ctx, fromStore(got))
	return out, added, err
}

// Remove removes an active number from a scope.
func (l BlacklistList) Remove(ctx context.Context, scope blacklist.Scope, group wa.JID, e164 string, by wa.JID) (bool, error) {
	who, err := optPerson(ctx, l.Persons, by)
	if err != nil {
		return false, fmt.Errorf("remover: %w", err)
	}
	return store.RemoveBlacklist(ctx, l.DB, rules.BlacklistScope(scope), string(group), e164, who, nowOf(l.Clock))
}

// ForGroup lists a group's entries, oldest first.
func (l BlacklistList) ForGroup(ctx context.Context, group wa.JID) ([]blacklist.Entry, error) {
	got, err := store.ListGroupBlacklist(ctx, l.DB, string(group))
	if err != nil {
		return nil, err
	}
	rows := make([]blRow, len(got))
	for i, e := range got {
		rows[len(got)-1-i] = fromStore(e) // the store lists newest first
	}
	return l.entries(ctx, rows)
}

// Instance lists the instance-wide entries, oldest first.
func (l BlacklistList) Instance(ctx context.Context) ([]blacklist.Entry, error) {
	got, err := db.New(l.DB).ListInstanceBlacklist(ctx)
	if err != nil {
		return nil, fmt.Errorf("list instance blacklist: %w", err)
	}
	rows := make([]blRow, len(got))
	for i, r := range got {
		rows[len(got)-1-i] = blRow{
			ID: r.ID, Scope: r.Scope, GroupJid: r.GroupJid, PnE164: r.PnE164, PersonID: r.PersonID,
			OnWhatsapp: r.OnWhatsapp, Reason: r.Reason, AddedByPerson: r.AddedByPerson, AddedAt: r.AddedAt,
		}
	}
	return l.entries(ctx, rows)
}

// ByNumber lists every active entry for a number.
func (l BlacklistList) ByNumber(ctx context.Context, e164 string) ([]blacklist.Entry, error) {
	got, err := db.New(l.DB).ListBlacklistByNumber(ctx, e164)
	if err != nil {
		return nil, fmt.Errorf("blacklist by number: %w", err)
	}
	rows := make([]blRow, len(got))
	for i, r := range got {
		rows[i] = blRow{
			ID: r.ID, Scope: r.Scope, GroupJid: r.GroupJid, PnE164: r.PnE164, PersonID: r.PersonID,
			OnWhatsapp: r.OnWhatsapp, Reason: r.Reason, AddedByPerson: r.AddedByPerson, AddedAt: r.AddedAt,
		}
	}
	return l.entries(ctx, rows)
}

// ByPerson lists every active entry resolved to the person with this LID.
func (l BlacklistList) ByPerson(ctx context.Context, lid wa.JID) ([]blacklist.Entry, error) {
	if lid == "" || l.Persons == nil {
		return nil, nil
	}
	var id int64
	if m, ok := l.Persons.(PersonMatcher); ok {
		found, known, err := m.MatchPersonID(ctx, lid)
		if err != nil || !known {
			return nil, err
		}
		id = found
	} else {
		var err error
		if id, err = l.Persons.PersonID(ctx, lid); err != nil {
			return nil, fmt.Errorf("person for %s: %w", lid, err)
		}
	}
	got, err := db.New(l.DB).ListBlacklistByPerson(ctx, pgtype.Int8{Int64: id, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("blacklist by person: %w", err)
	}
	rows := make([]blRow, len(got))
	for i, r := range got {
		rows[i] = blRow{
			ID: r.ID, Scope: r.Scope, GroupJid: r.GroupJid, PnE164: r.PnE164, PersonID: r.PersonID,
			OnWhatsapp: r.OnWhatsapp, Reason: r.Reason, AddedByPerson: r.AddedByPerson, AddedAt: r.AddedAt,
		}
	}
	return l.entries(ctx, rows)
}

// SetResolved records what resolution found on every active entry for the number.
func (l BlacklistList) SetResolved(ctx context.Context, e164 string, lid wa.JID, onWhatsApp bool) error {
	got, err := db.New(l.DB).ListBlacklistByNumber(ctx, e164)
	if err != nil {
		return fmt.Errorf("blacklist by number: %w", err)
	}
	pid, err := optPerson(ctx, l.Persons, lid)
	if err != nil {
		return fmt.Errorf("person for %s: %w", lid, err)
	}
	on := onWhatsApp
	var errs []error
	for _, r := range got {
		errs = append(errs, store.SetBlacklistIdentity(ctx, l.DB, r.ID, pid, &on))
	}
	return errors.Join(errs...)
}

// PhoneNormalizer is blacklist.Normalizer over WP-G's number parser (libphonenumber rules,
// F-123), the same one content matching uses.
type PhoneNormalizer struct{}

var _ blacklist.Normalizer = PhoneNormalizer{}

// Normalize returns the E.164 form of exactly one number.
func (PhoneNormalizer) Normalize(input, defaultCountry string) (string, error) {
	p := dblacklist.ParseArgs(input, defaultCountry)
	if len(p.Numbers) != 1 || len(p.Invalid) > 0 || p.Reason != "" {
		return "", fmt.Errorf("%q: %w", input, blacklist.ErrInvalid)
	}
	return p.Numbers[0], nil
}

// Corpus is review.Corpus over WP-G's confirmed-spam corpus (F-113): entries built by
// corpus.FromMessage, pattern confirmations for promotion, retraction and the fingerprint
// allow-list. corpus_sources remembers which message produced which entry.
type Corpus struct {
	Pool *pgxpool.Pool
	// HashKey is the deployment's number-hash key, the one the blacklist and number rules use:
	// both the hasher and redact.Text's [phone:<id>] tokens derive from it.
	HashKey []byte
	// Region returns a group's default country for numbers written without a code.
	Region  func(ctx context.Context, group wa.JID) string
	Persons PersonMap
	Clock   clock.Clock
}

var _ review.Corpus = Corpus{}

func (c Corpus) region(ctx context.Context, group wa.JID) string {
	if c.Region == nil {
		return ""
	}
	return c.Region(ctx, group)
}

// textEnvelope is the envelope for a confirmation that carries only text.
func textEnvelope(group wa.JID, msg wa.MessageRef, s string) *envelope.Envelope {
	sender := envelope.Identity{LID: msg.Sender}
	if !msg.Sender.IsLID() {
		sender = envelope.Identity{PN: msg.Sender}
	}
	return &envelope.Envelope{
		Group: group, MsgID: msg.ID, Sender: sender,
		Texts: []envelope.TextSurface{{Source: "text", Raw: s}},
	}
}

// Confirm stores the confirmed message as an active group entry and records its patterns.
func (c Corpus) Confirm(ctx context.Context, conf review.Confirmation) (int64, error) {
	env := conf.Envelope
	if env == nil {
		env = textEnvelope(conf.Group, conf.Message, conf.Text)
	}
	textnorm.Prepare(env, c.region(ctx, conf.Group))
	src := corpus.SourceCommand
	if conf.Source == "review" {
		src = corpus.SourceReview
	}
	at := conf.At
	if at.IsZero() {
		at = nowOf(c.Clock)
	}
	hasher, err := dblacklist.NewHasher(c.HashKey)
	if err != nil {
		return 0, fmt.Errorf("number hash key: %w", err)
	}
	e := corpus.FromMessage(env, corpus.Options{
		Scope: corpus.ScopeGroup, GroupJID: string(conf.Group), Status: corpus.StatusActive, Source: src,
		Region: c.region(ctx, conf.Group), Hasher: hasher, HashKey: c.HashKey, Confidence: 1, Now: at,
	})
	admin, err := optPerson(ctx, c.Persons, conf.Admin)
	if err != nil {
		return 0, fmt.Errorf("deciding admin: %w", err)
	}
	e.DecidedByPerson = admin
	if conf.ReviewID != 0 {
		id := conf.ReviewID
		e.SourceReviewID = &id
	}
	var entryID int64
	err = store.InTx(ctx, c.Pool, func(tx pgx.Tx) error {
		id, err := store.InsertCorpusEntry(ctx, tx, e)
		if err != nil {
			return err
		}
		entryID = id
		if err := db.New(tx).InsertCorpusSource(ctx, db.InsertCorpusSourceParams{
			GroupJid: string(conf.Group), MsgID: string(conf.Message.ID), CorpusEntryID: id,
		}); err != nil {
			return fmt.Errorf("record corpus source: %w", err)
		}
		e.ID = id
		for _, key := range corpus.PatternKeys(&e) {
			if _, err := store.ConfirmPattern(ctx, tx, key, string(conf.Group), admin, id, at); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("confirm spam %s: %w", conf.Message.ID, err)
	}
	return entryID, nil
}

// EntryFor returns the newest entry from a message that still matches.
func (c Corpus) EntryFor(ctx context.Context, group wa.JID, msg wa.MessageID) (int64, bool, error) {
	id, err := db.New(c.Pool).LiveCorpusEntryForMessage(ctx, db.LiveCorpusEntryForMessageParams{GroupJid: string(group), MsgID: string(msg)})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("corpus entry for %s: %w", msg, err)
	}
	return id, true, nil
}

// Retract removes an entry's learned rules and text and marks it retracted (§7.5).
func (c Corpus) Retract(ctx context.Context, id int64) error {
	if _, err := store.RetractCorpusEntry(ctx, c.Pool, id); err != nil {
		return fmt.Errorf("retract corpus entry %d: %w", id, err)
	}
	return nil
}

// AllowFingerprint stores an overturned message's fingerprints as group allow-list entries
// (§7.5): the retracted entry's SimHash and media hashes when there is an entry, otherwise a
// SimHash computed from the text. With neither it stores nothing, so empty text never
// allow-lists anything. Domains and number hashes are never allow-listed: an allow entry beats
// a hard deny, and a domain or number is shared by many messages.
func (c Corpus) AllowFingerprint(ctx context.Context, f review.FingerprintAllow) error {
	var h fingerprint.SimHash
	ok := false
	var allow []rules.Fingerprint
	if f.EntryID != 0 {
		e, err := store.GetCorpusEntry(ctx, c.Pool, f.EntryID)
		if err != nil {
			return fmt.Errorf("corpus entry %d: %w", f.EntryID, err)
		}
		if e.SimHash != nil {
			h, ok = *e.SimHash, true
		}
		for _, m := range e.MediaSHA256 {
			allow = append(allow, rules.Fingerprint{Kind: rules.FPSHA256, Value: hex.EncodeToString(m)})
		}
	}
	if !ok && strings.TrimSpace(f.Text) != "" {
		env := textEnvelope(f.Group, f.Message, f.Text)
		textnorm.Prepare(env, c.region(ctx, f.Group))
		h, ok = fingerprint.ComputeSimHash(env.Texts[0].Norm)
	}
	if ok {
		allow = append(allow, rules.Fingerprint{Kind: rules.FPSimHash, Value: fmt.Sprintf("%016x", uint64(h))})
	}
	var errs []error
	for _, fp := range allow {
		fp.Label, fp.Source, fp.Scope, fp.ScopeID = rules.LabelAllow, rules.SourceAdmin, rules.ScopeGroup, string(f.Group)
		if err := store.UpsertFingerprint(ctx, c.Pool, fp, nowOf(c.Clock)); err != nil {
			errs = append(errs, fmt.Errorf("allow-list %s fingerprint: %w", fp.Kind, err))
		}
	}
	return errors.Join(errs...)
}

// Lists is chat.Lists over WP-G's rules table (F-042, F-048): group domain allow/block rules
// and blocked words, which every node reloads on NOTIFY.
type Lists struct {
	DB      db.DBTX
	Persons PersonMap
	// Norm is the text normalizer keyword patterns go through, so a blocked word matches the way
	// the detector reads messages. nil uses the detector's own (textnorm.Prepare's Norm).
	Norm func(string) string
}

// norm returns the normalizer patterns go through.
func (l Lists) norm() func(string) string {
	if l.Norm != nil {
		return l.Norm
	}
	return func(s string) string {
		env := &envelope.Envelope{Texts: []envelope.TextSurface{{Raw: s}}}
		textnorm.Prepare(env, "")
		return env.Texts[0].Norm
	}
}

// inTx runs fn in a transaction when DB can start one (a pool), so an allow/block switch
// never leaves both rules.
func (l Lists) inTx(ctx context.Context, fn func(q db.DBTX) error) error {
	if b, ok := l.DB.(store.TxBeginner); ok {
		return store.InTx(ctx, b, func(tx pgx.Tx) error { return fn(tx) })
	}
	return fn(l.DB)
}

func (l Lists) find(ctx context.Context, q db.DBTX, group wa.JID, t rules.Type, norm string, class rules.ActionClass) (int64, bool, error) {
	id, err := db.New(q).FindGroupRule(ctx, db.FindGroupRuleParams{
		ScopeID: string(group), Type: string(t), NormPattern: norm, ActionClass: string(class),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("find rule: %w", err)
	}
	return id, true, nil
}

// put adds a group rule of class (if missing) and removes the opposite class for the same
// pattern, so allowing a blocked domain unblocks it and vice versa.
func (l Lists) put(ctx context.Context, group wa.JID, t rules.Type, pattern string, class, opposite rules.ActionClass, actor wa.JID) error {
	norm, err := rules.NormalizePattern(t, pattern, l.norm())
	if err != nil {
		return fmt.Errorf("%q: %w", pattern, err)
	}
	if t == rules.TypeDomain {
		// "www.example.com" and "example.com" are one site to an admin.
		if rest := strings.TrimPrefix(norm, "www."); strings.Contains(rest, ".") {
			norm = rest
		}
		pattern = norm
	}
	by, err := optPerson(ctx, l.Persons, actor)
	if err != nil {
		return fmt.Errorf("rule author: %w", err)
	}
	return l.inTx(ctx, func(q db.DBTX) error {
		if opposite != "" {
			if id, ok, err := l.find(ctx, q, group, t, norm, opposite); err != nil {
				return err
			} else if ok {
				if _, err := store.DeleteRule(ctx, q, id); err != nil {
					return err
				}
			}
		}
		if _, ok, err := l.find(ctx, q, group, t, norm, class); err != nil || ok {
			return err
		}
		_, err := store.InsertRule(ctx, q, rules.Rule{
			Scope: rules.ScopeGroup, ScopeID: string(group), Type: t, Pattern: pattern, NormPattern: norm,
			ActionClass: class, Enabled: true,
		}, by)
		return err
	})
}

// AllowDomain allow-lists a domain in the group (and drops a block of it).
func (l Lists) AllowDomain(ctx context.Context, group wa.JID, domain string, actor wa.JID) error {
	return l.put(ctx, group, rules.TypeDomain, domain, rules.ActionAllow, rules.ActionHard, actor)
}

// BlockDomain blocks a domain in the group (and drops an allow of it).
func (l Lists) BlockDomain(ctx context.Context, group wa.JID, domain string, actor wa.JID) error {
	return l.put(ctx, group, rules.TypeDomain, domain, rules.ActionHard, rules.ActionAllow, actor)
}

func wordType(phrase string) rules.Type {
	if len(strings.Fields(phrase)) > 1 {
		return rules.TypePhrase
	}
	return rules.TypeKeyword
}

// BlockWord blocks a word or phrase in the group.
func (l Lists) BlockWord(ctx context.Context, group wa.JID, phrase string, actor wa.JID) error {
	return l.put(ctx, group, wordType(phrase), phrase, rules.ActionHard, "", actor)
}

// UnblockWord removes a blocked word or phrase.
func (l Lists) UnblockWord(ctx context.Context, group wa.JID, phrase string, _ wa.JID) error {
	t := wordType(phrase)
	norm, err := rules.NormalizePattern(t, phrase, l.norm())
	if err != nil {
		return fmt.Errorf("%q: %w", phrase, err)
	}
	id, ok, err := l.find(ctx, l.DB, group, t, norm, rules.ActionHard)
	if err != nil || !ok {
		return err
	}
	_, err = store.DeleteRule(ctx, l.DB, id)
	return err
}
