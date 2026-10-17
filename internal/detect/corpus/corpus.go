// Package corpus builds confirmed-spam corpus entries (DESIGN.md §7.5): what is kept from a
// message once it is confirmed as spam. Text is redacted before it is stored, numbers are
// kept only as keyed hashes, and the label is always spam: the bot keeps no normal-message
// corpus (§7.5, §17). Storage lives in internal/store.
package corpus

import (
	"encoding/hex"
	"slices"
	"strings"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/fingerprint"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/links"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize/redact"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize/urls"
)

// Kind is what an entry was made from.
type Kind string

// Kinds.
const (
	KindMessage Kind = "message"
	KindProfile Kind = "profile"
)

// LabelSpam is the only label an entry can have.
const LabelSpam = "spam"

// Scope says where an entry applies: the group it was confirmed in, or every group that opted
// into network intelligence once promoted (§7.5 "Local first, global later").
type Scope string

// Scopes.
const (
	ScopeGroup  Scope = "group"
	ScopeGlobal Scope = "global"
)

// Status is an entry's lifecycle state.
type Status string

// Statuses. Provisional entries carry less weight and never hard-block (§7.5).
const (
	StatusProvisional Status = "provisional"
	StatusActive      Status = "active"
	StatusRetracted   Status = "retracted"
	StatusPromoted    Status = "promoted"
	StatusExpired     Status = "expired"
)

// Source is the learning event that created an entry (§7.5).
type Source string

// Sources.
const (
	SourceReview  Source = "review"
	SourceCommand Source = "command"
	SourceAuto    Source = "auto"
	SourceImport  Source = "import"
)

// Retention is the default lifetime of an entry's raw text (§7.5, §14); fingerprints are
// kept while they keep matching, which the expiry job decides from LastHitAt.
const Retention = 180 * 24 * time.Hour

// Entry is one corpus_entries row.
type Entry struct {
	ID              int64
	Kind            Kind
	Scope           Scope
	GroupJID        string
	Status          Status
	Source          Source
	ImportBatchID   *int64
	SourceReviewID  *int64
	SourceActionID  *int64
	DecidedByPerson *int64

	TextRawRedacted string
	TextNorm        string
	SimHash         *fingerprint.SimHash // nil when the message has no text
	MinHash         []byte               // fingerprint.MinHashBytes long, or nil
	Keywords        []string
	URLs            []string // canonical URLs
	Domains         []string // eTLD+1
	NumberHashes    [][]byte // keyed hashes of advertised numbers
	ChannelJIDs     []string
	InviteCodes     []string
	MediaSHA256     [][]byte
	PHashes         []int64
	Confidence      float64
	Hits            int
	LastHitAt       *time.Time
	CreatedAt       time.Time
	ExpiresAt       time.Time
}

// Options are the inputs of FromMessage besides the envelope.
type Options struct {
	Scope    Scope
	GroupJID string
	Status   Status
	Source   Source
	// Region is the group's default country, for finding numbers in the text.
	Region string
	// Hasher hashes advertised numbers; the same key as the blacklist and number rules.
	Hasher blacklist.Hasher
	// HashKey is the same key, for redact.Text's [phone:<id>] tokens.
	HashKey []byte
	// Names are names to mask in the stored text (the sender's and mentioned members'
	// push names).
	Names []string
	// Keywords are the listed keywords and phrases the message matched, if any.
	Keywords   []string
	Confidence float64
	Now        time.Time
	Retention  time.Duration // 0 means Retention
}

// FromMessage builds the entry for a confirmed spam message. e must have been through
// textnorm.Prepare (e.Phones are the advertised numbers). Each text surface is redacted with
// redact.Text, the spam's own advertised numbers becoming keyed-hash tokens and third-party
// numbers, emails, mentions and names being masked; the normalized text and both fingerprints
// are computed from the redacted text, so nothing masked is stored in another form.
func FromMessage(e *envelope.Envelope, o Options) Entry {
	ent := Entry{
		Kind: KindMessage, Scope: o.Scope, GroupJID: o.GroupJID, Status: o.Status, Source: o.Source,
		Keywords: slices.Clone(o.Keywords), Confidence: o.Confidence, CreatedAt: o.Now,
	}
	ret := o.Retention
	if ret <= 0 {
		ret = Retention
	}
	ent.ExpiresAt = o.Now.Add(ret)

	numbers := blacklist.Numbers(e)
	advertised := make([]string, 0, len(numbers))
	for _, n := range numbers {
		advertised = append(advertised, n.E164)
		if b, err := hex.DecodeString(o.Hasher.Hash(n.E164)); err == nil {
			ent.NumberHashes = append(ent.NumberHashes, b)
		}
	}
	ro := redact.Options{Region: o.Region, Key: o.HashKey, Advertised: advertised, Names: o.Names}
	var raw, norm []string
	for i := range e.Texts {
		r := redact.Text(e.Texts[i].Raw, ro)
		if strings.TrimSpace(r) == "" {
			continue
		}
		raw = append(raw, r)
		norm = append(norm, normalize.Text(r).Norm)
	}
	ent.TextRawRedacted = strings.Join(raw, "\n")
	ent.TextNorm = strings.Join(norm, "\n")
	if h, ok := fingerprint.ComputeSimHash(ent.TextNorm); ok {
		ent.SimHash = &h
	}
	if m, ok := fingerprint.ComputeMinHash(ent.TextNorm); ok {
		ent.MinHash = m.Bytes()
	}

	for _, l := range links.Collect(e) {
		// A wa.me or api.whatsapp.com/send link carries the number in the clear; its keyed hash
		// is already in NumberHashes. Other links are redacted like the text, so numbers,
		// emails and mentions in their paths or queries aren't kept either.
		if l.Target == nil || l.Target.Kind != urls.TargetPhone {
			ent.URLs = appendNew(ent.URLs, redact.Text(l.Canonical, ro))
		}
		// A WhatsApp-native link's domain is WhatsApp's own: learning it would block every
		// invite and channel link. Its target is kept instead.
		if l.Domain != "" && l.Target == nil {
			ent.Domains = appendNew(ent.Domains, l.Domain)
		}
		if l.Target != nil {
			switch l.Target.Kind {
			case urls.TargetChannel:
				ent.ChannelJIDs = appendNew(ent.ChannelJIDs, l.Target.ID)
			case urls.TargetGroupInvite:
				ent.InviteCodes = appendNew(ent.InviteCodes, l.Target.ID)
			case urls.TargetPhone, urls.TargetMessage, urls.TargetQR:
				// Numbers are kept as keyed hashes above; message and QR codes resolve to
				// nothing stored here.
			}
		}
	}
	for _, c := range e.Channels {
		if c.JID != "" {
			ent.ChannelJIDs = appendNew(ent.ChannelJIDs, string(c.JID))
		}
	}
	for _, in := range e.Invites {
		if in.Code != "" {
			ent.InviteCodes = appendNew(ent.InviteCodes, in.Code)
		}
	}
	for _, m := range e.Media {
		if len(m.SHA256) > 0 && !slices.ContainsFunc(ent.MediaSHA256, func(b []byte) bool { return slices.Equal(b, m.SHA256) }) {
			ent.MediaSHA256 = append(ent.MediaSHA256, slices.Clone(m.SHA256))
		}
	}
	return ent
}

func appendNew(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}

// PatternKeys returns the patterns an entry consists of, as the keys corpus_confirmations
// counts across groups for promotion (§7.5: "same domain/number/channel/hash"): one key per
// domain, number hash, channel, invite and media hash, plus the SimHash. Keys are stable
// strings: "<kind>:<value>".
func PatternKeys(e *Entry) []string {
	var keys []string
	for _, d := range e.Domains {
		keys = append(keys, "domain:"+d)
	}
	for _, n := range e.NumberHashes {
		keys = append(keys, "number:"+hex.EncodeToString(n))
	}
	for _, c := range e.ChannelJIDs {
		keys = append(keys, "channel:"+c)
	}
	for _, c := range e.InviteCodes {
		keys = append(keys, "invite:"+c)
	}
	for _, m := range e.MediaSHA256 {
		keys = append(keys, "sha256:"+hex.EncodeToString(m))
	}
	if e.SimHash != nil {
		keys = append(keys, "simhash:"+hex.EncodeToString(simBytes(*e.SimHash)))
	}
	return keys
}

func simBytes(h fingerprint.SimHash) []byte {
	b := make([]byte, 8)
	for i := range 8 {
		b[i] = byte(uint64(h) >> (56 - 8*i))
	}
	return b
}
