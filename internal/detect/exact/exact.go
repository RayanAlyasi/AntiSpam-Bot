// Package exact is the T0 analyzer for exact known-spam values (DESIGN.md §7.1, §6.2, §6.4,
// §6.8, §6.9): media and sticker SHA256s, channel JIDs and codes, group invite codes and
// advertised phone numbers (as keyed hashes) that are blocklisted by a rule or confirmed as
// spam fingerprints. It reads only what the envelope already carries; nothing is downloaded.
package exact

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/links"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/ruleview"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize/urls"
)

// Signal codes. The *_known codes are known-spam evidence (CERTAIN, §7.0); the *_listed
// codes come from weighted rules.
const (
	CodeMediaKnown    = "media.known_hash"
	CodeStickerKnown  = "sticker.known_hash"
	CodeChannelKnown  = "channel.blocklisted"
	CodeInviteKnown   = "invite.blocklisted"
	CodeNumberKnown   = "number.blocklisted"
	CodeMediaListed   = "media.listed_hash"
	CodeStickerListed = "sticker.listed_hash"
	CodeChannelListed = "channel.listed"
	CodeInviteListed  = "invite.listed"
	CodeNumberListed  = "number.listed"
)

// Analyzer is the exact-match analyzer (T0).
type Analyzer[P any] struct {
	src  ruleview.Source
	hash func(e164 string) string
}

// New returns the analyzer. hash is the keyed number hash (blacklist.Hasher.Hash), the same
// one used to store number rules and fingerprints.
func New[P any](src ruleview.Source, hash func(e164 string) string) *Analyzer[P] {
	return &Analyzer[P]{src: src, hash: hash}
}

// Name implements detect.Analyzer.
func (*Analyzer[P]) Name() string { return "exact" }

// Tier implements detect.Analyzer.
func (*Analyzer[P]) Tier() detect.Tier { return detect.T0 }

// Applies implements detect.Analyzer.
func (*Analyzer[P]) Applies(e *envelope.Envelope, _ *P) bool {
	return len(e.Media) > 0 || len(e.Channels) > 0 || len(e.Invites) > 0 || len(e.Phones) > 0 ||
		len(e.URLs) > 0 || len(e.Texts) > 0 || len(e.Contacts) > 0 || e.Ads != nil || e.Status != nil
}

type check struct {
	kind                  rules.FingerprintKind
	value                 string
	what                  string // evidence, safe to show (numbers masked)
	knownCode, listedCode string
}

// Analyze implements detect.Analyzer. One signal per code: the first value that produced it.
func (a *Analyzer[P]) Analyze(_ context.Context, e *envelope.Envelope, _ *detect.State) ([]detect.Signal, error) {
	v := a.src.View(e)
	var out []detect.Signal
	emitted := map[string]bool{}
	for _, c := range a.checks(e) {
		r := v.Exact(c.kind, c.value)
		if !r.Denied() {
			continue
		}
		var s detect.Signal
		switch {
		case r.Rule != nil && r.Rule.ActionClass == rules.ActionWeighted:
			s = detect.Signal{
				Code: c.listedCode, Weight: r.Rule.Weight,
				Evidence: fmt.Sprintf("%s is on the %s watch list", c.what, r.Scope),
			}
		case r.Rule != nil:
			s = detect.Signal{
				Code: c.knownCode, Weight: rules.KnownWeight(r.Rule), Known: true,
				Evidence: fmt.Sprintf("%s is blocklisted (%s list)", c.what, r.Scope),
			}
		default:
			s = detect.Signal{
				Code: c.knownCode, Weight: rules.KnownWeight(nil), Known: true,
				Evidence: fmt.Sprintf("%s matches confirmed spam (%s)", c.what, r.Scope),
			}
		}
		if !emitted[s.Code] {
			emitted[s.Code] = true
			out = append(out, s)
		}
	}
	return out, nil
}

// checks lists every exact value of e: media hashes, channels, invites, then numbers.
func (a *Analyzer[P]) checks(e *envelope.Envelope) []check {
	var cs []check
	for _, m := range e.Media {
		if len(m.SHA256) == 0 {
			continue
		}
		h := hex.EncodeToString(m.SHA256)
		short := h[:min(len(h), 12)]
		if m.Type == envelope.KindSticker {
			cs = append(cs, check{rules.FPSHA256, h, "sticker " + short, CodeStickerKnown, CodeStickerListed})
		} else {
			cs = append(cs, check{rules.FPSHA256, h, fmt.Sprintf("%s %s", m.Type, short), CodeMediaKnown, CodeMediaListed})
		}
	}
	channel := func(v, src string) check {
		return check{rules.FPChannel, v, fmt.Sprintf("channel %s (%s)", v, src), CodeChannelKnown, CodeChannelListed}
	}
	invite := func(v, src string) check {
		return check{rules.FPInvite, v, fmt.Sprintf("group invite %s (%s)", v, src), CodeInviteKnown, CodeInviteListed}
	}
	for _, c := range e.Channels {
		for _, v := range []string{string(c.JID), c.InviteCode} {
			if v != "" {
				cs = append(cs, channel(v, c.Source))
			}
		}
	}
	for _, in := range e.Invites {
		for _, v := range []string{in.Code, string(in.Group)} {
			if v != "" {
				cs = append(cs, invite(v, in.Source))
			}
		}
	}
	for _, l := range links.Collect(e) {
		if l.Target == nil {
			continue
		}
		switch l.Target.Kind {
		case urls.TargetChannel:
			cs = append(cs, channel(l.Target.ID, "link"))
		case urls.TargetGroupInvite:
			cs = append(cs, invite(l.Target.ID, "link"))
		case urls.TargetPhone, urls.TargetMessage, urls.TargetQR:
			// Numbers are checked below with the others; message and QR codes need resolving.
		}
	}
	for _, n := range blacklist.Numbers(e) {
		cs = append(cs, check{
			rules.FPNumber, a.hash(n.E164),
			fmt.Sprintf("advertised number %s (%s)", blacklist.Mask(n.E164), n.Source), CodeNumberKnown, CodeNumberListed,
		})
	}
	return cs
}
