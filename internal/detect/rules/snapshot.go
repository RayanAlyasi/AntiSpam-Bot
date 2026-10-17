package rules

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/textmatch"
)

// Text is one text surface in the normalized variants keyword, phrase and regex rules match
// (DESIGN.md §7.2): Norm, the confusables Skeleton, the Leet fold and the Arabic Loose form.
// An empty variant is skipped.
type Text struct {
	Norm, Skeleton, Leet, Loose string
}

const numVariants = 4

func (t Text) variants() [numVariants]string {
	return [numVariants]string{t.Norm, t.Skeleton, t.Leet, t.Loose}
}

type layerKey struct {
	scope Scope
	id    string
	pack  string // global rules only
}

// layer is the compiled entries of one scope (and, for global rules, one pack).
type layer struct {
	kwRules []*Rule
	kw      *textmatch.Automaton
	phrases []fuzzyRule
	regexes []regexRule
	domains map[string]domainEntry
	urls    map[string]domainEntry // scheme-less URL prefixes
	exact   map[FingerprintKind]map[string]exactEntry
}

type fuzzyRule struct {
	rule  *Rule
	words string
}

type regexRule struct {
	rule *Rule
	re   *regexp.Regexp
}

type domainEntry struct {
	allow bool
	deny  *Rule // strongest deny rule for the value
}

type exactEntry struct {
	allow bool
	deny  *Rule
	fp    *Fingerprint // spam-labelled fingerprint, when no deny rule
}

// Snapshot is an immutable compiled view of every enabled rule, fingerprint and blacklist
// entry. It is safe for concurrent use; the Holder replaces it as a whole on change.
type Snapshot struct {
	layers    map[layerKey]*layer
	blGroup   map[string]map[string]*BlacklistEntry // group JID → hash → entry
	blInst    map[string]*BlacklistEntry
	rules     int
	fps       int
	blacklist int
}

// Empty is the snapshot before anything is loaded: nothing matches.
func Empty() *Snapshot {
	return &Snapshot{layers: map[layerKey]*layer{}, blGroup: map[string]map[string]*BlacklistEntry{}, blInst: map[string]*BlacklistEntry{}}
}

// Counts reports how many enabled rules, fingerprints and blacklist entries were compiled.
func (s *Snapshot) Counts() (rules, fingerprints, blacklist int) { return s.rules, s.fps, s.blacklist }

// Compile builds a snapshot. Disabled rules are skipped. An entry that fails validation (a
// row written before a limit existed, or edited by hand) is skipped and reported in errs,
// so one bad row never disables the rest.
func Compile(rs []Rule, fps []Fingerprint, bl []BlacklistEntry) (s *Snapshot, errs []error) {
	s = Empty()
	kws := map[layerKey][]textmatch.Keyword{}
	get := func(k layerKey) *layer {
		l := s.layers[k]
		if l == nil {
			l = &layer{domains: map[string]domainEntry{}, urls: map[string]domainEntry{}, exact: map[FingerprintKind]map[string]exactEntry{}}
			s.layers[k] = l
		}
		return l
	}
	for i := range rs {
		r := rs[i]
		if !r.Enabled {
			continue
		}
		if err := r.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("rule %d: %w", r.ID, err))
			continue
		}
		key := layerKey{scope: r.Scope, id: r.ScopeID}
		if r.Scope == ScopeGlobal {
			key.pack = r.Pack
		}
		l := get(key)
		s.rules++
		switch r.Type {
		case TypeKeyword, TypePhrase:
			l.kwRules = append(l.kwRules, &r)
			kws[key] = append(kws[key], textmatch.Keyword{Text: r.NormPattern, WholeWord: true})
			if r.Type == TypePhrase && utf8.RuneCountInString(r.NormPattern) >= textmatch.MinFuzzyRunes {
				l.phrases = append(l.phrases, fuzzyRule{rule: &r, words: r.NormPattern})
			}
		case TypeRegex:
			re, err := textmatch.CompileRegex(r.Pattern)
			if err != nil { // Validate already compiled it; kept for safety
				errs = append(errs, fmt.Errorf("rule %d: %w", r.ID, err))
				s.rules--
				continue
			}
			l.regexes = append(l.regexes, regexRule{rule: &r, re: re})
		case TypeDomain:
			l.domains[r.NormPattern] = mergeDomain(l.domains[r.NormPattern], &r)
		case TypeURLPrefix:
			l.urls[r.NormPattern] = mergeDomain(l.urls[r.NormPattern], &r)
		case TypePhone, TypeChannel, TypeInvite, TypeSHA256:
			kind := typeKind[r.Type]
			m := l.exact[kind]
			if m == nil {
				m = map[string]exactEntry{}
				l.exact[kind] = m
			}
			m[r.NormPattern] = mergeExactRule(m[r.NormPattern], &r)
		case TypePHash, TypeSimHash:
			// Loaded and counted; perceptual and near-duplicate matching use their own indexes.
		}
	}
	for k, list := range kws {
		s.layers[k].kw = textmatch.NewAutomaton(list)
	}
	for i := range fps {
		f := fps[i]
		if err := f.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("fingerprint %s/%s: %w", f.Kind, f.Value, err))
			continue
		}
		s.fps++
		l := get(layerKey{scope: f.Scope, id: f.ScopeID})
		m := l.exact[f.Kind]
		if m == nil {
			m = map[string]exactEntry{}
			l.exact[f.Kind] = m
		}
		e := m[f.Value]
		if f.Label == LabelAllow {
			e.allow = true
		} else if e.fp == nil {
			e.fp = &f
		}
		m[f.Value] = e
	}
	for i := range bl {
		e := bl[i]
		if e.PNHash == "" || !isHex(e.PNHash) || (e.Scope == BlacklistGroup) == (e.GroupJID == "") ||
			(e.Scope != BlacklistGroup && e.Scope != BlacklistInstance) {
			errs = append(errs, fmt.Errorf("blacklist entry %d: %w", e.ID, ErrInvalidPattern))
			continue
		}
		s.blacklist++
		if e.Scope == BlacklistInstance {
			s.blInst[e.PNHash] = &e
			continue
		}
		g := s.blGroup[e.GroupJID]
		if g == nil {
			g = map[string]*BlacklistEntry{}
			s.blGroup[e.GroupJID] = g
		}
		g[e.PNHash] = &e
	}
	return s, errs
}

var typeKind = map[Type]FingerprintKind{
	TypePhone:   FPNumber,
	TypeChannel: FPChannel,
	TypeInvite:  FPInvite,
	TypeSHA256:  FPSHA256,
}

// stronger reports whether a should replace b as the deny rule for a value: hard beats
// weighted, then the larger weight, then the lower ID (deterministic).
func stronger(a, b *Rule) bool {
	if b == nil {
		return true
	}
	if (a.ActionClass == ActionHard) != (b.ActionClass == ActionHard) {
		return a.ActionClass == ActionHard
	}
	if a.Weight != b.Weight {
		return a.Weight > b.Weight
	}
	return a.ID < b.ID
}

func mergeDomain(e domainEntry, r *Rule) domainEntry {
	if r.ActionClass == ActionAllow {
		e.allow = true
	} else if stronger(r, e.deny) {
		e.deny = r
	}
	return e
}

func mergeExactRule(e exactEntry, r *Rule) exactEntry {
	if r.ActionClass == ActionAllow {
		e.allow = true
	} else if stronger(r, e.deny) {
		e.deny = r
	}
	return e
}

// ViewOptions selects the scopes that apply to one message.
type ViewOptions struct {
	Bot, Group string
	// Packs are the global packs the group subscribes to (§12.3, blocklist_refs). Global
	// rules without a pack always apply.
	Packs []string
	// AllowDomains are the group settings' allow-listed domains (allowlist.domains and
	// content.link_domains.allow, §12.1). They take precedence over every rule.
	AllowDomains []string
	// UseInstanceBlacklist is the group's blacklist.use_instance setting (§9.8).
	UseInstanceBlacklist bool
}

// View is a snapshot narrowed to one group: its layers in precedence order, group first,
// then bot, then global.
type View struct {
	s      *Snapshot
	o      ViewOptions
	layers []scoped
	allow  map[string]bool
}

type scoped struct {
	scope Scope
	l     *layer
}

// View returns the rules that apply to a message in o.Group of o.Bot.
func (s *Snapshot) View(o ViewOptions) View {
	v := View{s: s, o: o}
	add := func(k layerKey) {
		if l := s.layers[k]; l != nil {
			v.layers = append(v.layers, scoped{scope: k.scope, l: l})
		}
	}
	if o.Group != "" {
		add(layerKey{scope: ScopeGroup, id: o.Group})
	}
	if o.Bot != "" {
		add(layerKey{scope: ScopeBot, id: o.Bot})
	}
	add(layerKey{scope: ScopeGlobal})
	packs := slices.Clone(o.Packs)
	slices.Sort(packs)
	for _, p := range slices.Compact(packs) {
		if p != "" {
			add(layerKey{scope: ScopeGlobal, pack: p})
		}
	}
	for _, d := range o.AllowDomains {
		if n, err := NormalizeDomain(d); err == nil {
			if v.allow == nil {
				v.allow = map[string]bool{}
			}
			v.allow[n] = true
		}
	}
	return v
}

// TextHit is a keyword, phrase, regex or fuzzy rule match.
type TextHit struct {
	Rule    *Rule
	Scope   Scope
	Matched string // the matched text (normalized), for evidence
	// Similarity is 1 for exact matches and the fuzzy similarity otherwise.
	Similarity float64
}

// Keywords returns the keyword and phrase rules that match t in any variant, one hit per
// rule, in layer order then rule ID. The confusables Skeleton is only matched when Norm has a
// non-ASCII letter: on plain ASCII text it only adds false matches ("com" skeletonizes to
// "corn", "101" to "lol"), while cross-script look-alikes always involve a non-ASCII letter.
func (v View) Keywords(t Text) []TextHit {
	var out []TextHit
	vars := t.variants()
	if !hasNonASCIILetter(t.Norm) {
		vars[1] = ""
	}
	var words [numVariants]string
	for i, text := range vars {
		words[i] = textmatch.Words(text)
	}
	for _, sl := range v.layers {
		if sl.l.kw == nil {
			continue
		}
		hit := map[int]string{}
		for _, w := range words {
			if w == "" {
				continue
			}
			for _, h := range sl.l.kw.Find(w) {
				if _, ok := hit[h.Pattern]; !ok {
					hit[h.Pattern] = w[h.Start:h.End]
				}
			}
		}
		start := len(out)
		for i, m := range hit {
			out = append(out, TextHit{Rule: sl.l.kwRules[i], Scope: sl.scope, Matched: m, Similarity: 1})
		}
		sortHits(out[start:])
	}
	return out
}

func hasNonASCIILetter(s string) bool {
	for _, r := range s {
		if r >= utf8.RuneSelf && unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// maxFuzzyText bounds the text Fuzzy tokenizes: far more than MaxFuzzyTokens words need.
const maxFuzzyText = 16 * textmatch.MaxFuzzyTokens

// MaxRegexText is how much of a text, in bytes, regex rules scan. RE2 is linear, but every
// rule rescans the text, so a padded message times many rules would blow the T0 budget.
// Keyword, link and number checks still see the whole text.
const MaxRegexText = 4096

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Regexes returns the regex rules that match the first MaxRegexText bytes of t's Norm or
// Skeleton (after textmatch.Prepare), one hit per rule, in layer order then rule ID.
func (v View) Regexes(t Text) []TextHit {
	var out []TextHit
	texts := []string{clip(textmatch.Prepare(t.Norm), MaxRegexText)}
	if t.Skeleton != "" && t.Skeleton != t.Norm && hasNonASCIILetter(t.Norm) {
		texts = append(texts, clip(textmatch.Prepare(t.Skeleton), MaxRegexText))
	}
	for _, sl := range v.layers {
		start := len(out)
		for _, rr := range sl.l.regexes {
			for _, text := range texts {
				if loc := rr.re.FindStringIndex(text); loc != nil {
					out = append(out, TextHit{Rule: rr.rule, Scope: sl.scope, Matched: text[loc[0]:loc[1]], Similarity: 1})
					break
				}
			}
		}
		sortHits(out[start:])
	}
	return out
}

// Fuzzy returns the phrase rules with a near-miss in t's Norm, Leet or Loose variant
// (textmatch.Windows, which looks at the first textmatch.MaxFuzzyTokens words), the best hit
// per rule, in layer order then rule ID. A variant where the phrase occurs exactly gives no
// fuzzy hit (exact matches are Keywords'; callers drop rules Keywords matched anywhere). It
// stops with ctx's error when ctx ends.
func (v View) Fuzzy(ctx context.Context, t Text) ([]TextHit, error) {
	var out []TextHit
	var texts []string
	for _, s := range []string{t.Norm, t.Leet, t.Loose} {
		// MaxFuzzyTokens words never need more than this much text.
		if w := textmatch.Words(clip(s, maxFuzzyText)); w != "" && !slices.Contains(texts, w) {
			texts = append(texts, w)
		}
	}
	wins := make([]*textmatch.Windows, len(texts))
	for i, text := range texts {
		wins[i] = textmatch.NewWindows(text)
	}
	for _, sl := range v.layers {
		start := len(out)
		for _, fr := range sl.l.phrases {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			best, found := textmatch.FuzzyHit{}, false
			for _, w := range wins {
				if h, ok := w.Find(fr.words); ok && (!found || h.Similarity > best.Similarity) {
					best, found = h, true
				}
			}
			if found {
				out = append(out, TextHit{Rule: fr.rule, Scope: sl.scope, Matched: best.Matched, Similarity: best.Similarity})
			}
		}
		sortHits(out[start:])
	}
	return out, nil
}

func sortHits(h []TextHit) {
	slices.SortFunc(h, func(a, b TextHit) int { return cmp.Compare(a.Rule.ID, b.Rule.ID) })
}

// ListResult is the outcome of a domain, URL prefix or exact-value lookup.
type ListResult struct {
	// Allowed is true when an allow entry decided the lookup: nothing may fire.
	Allowed bool
	// Rule is the deny rule that decided it, or nil.
	Rule *Rule
	// Fingerprint is the spam fingerprint that decided it when no rule did, or nil.
	Fingerprint *Fingerprint
	// Scope is the scope of the deciding entry.
	Scope Scope
	// Matched is the listed value that matched (a parent domain, a URL prefix).
	Matched string
}

// Denied reports whether a deny rule or spam fingerprint decided the lookup.
func (r ListResult) Denied() bool { return r.Rule != nil || r.Fingerprint != nil }

// Domain checks a host (lower-case ASCII, as normalize/urls gives it) against the domain
// lists: the group settings' allow-list, then each layer in precedence order (group, bot,
// global). Within a layer the most specific listed domain counts (a.example.com before
// example.com), and an allow entry beats a deny entry for the same domain. An allow or a
// hard deny decides; a weighted deny is kept but the walk goes on, so a wider hard deny
// (known evidence) or allow still decides. So a group's allow-list overrides a global
// deny-list, and a group's hard deny-list overrides a global allow-list.
func (v View) Domain(host string) ListResult {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return ListResult{}
	}
	for d := range parents(host) {
		if v.allow[d] {
			return ListResult{Allowed: true, Scope: ScopeGroup, Matched: d}
		}
	}
	var weighted ListResult
	for _, sl := range v.layers {
		for d := range parents(host) {
			e, ok := sl.l.domains[d]
			if !ok {
				continue
			}
			if r, done := decide(e, sl.scope, d, &weighted); done {
				return r
			}
			break // the most specific entry of this layer counted
		}
	}
	return weighted
}

// URL checks a scheme-less canonical URL ("example.com/path?q") against the URL-prefix
// lists, with the same precedence as Domain; within a layer the longest prefix counts.
func (v View) URL(u string) ListResult {
	var weighted ListResult
	for _, sl := range v.layers {
		best := ""
		for p := range sl.l.urls {
			if strings.HasPrefix(u, p) && len(p) > len(best) {
				best = p
			}
		}
		if best == "" {
			continue
		}
		if r, done := decide(sl.l.urls[best], sl.scope, best, &weighted); done {
			return r
		}
	}
	return weighted
}

// decide applies one layer's entry: an allow or a hard deny decides; the first weighted
// deny is remembered in weighted.
func decide(e domainEntry, scope Scope, matched string, weighted *ListResult) (ListResult, bool) {
	switch {
	case e.allow:
		return ListResult{Allowed: true, Scope: scope, Matched: matched}, true
	case e.deny != nil && e.deny.ActionClass == ActionHard:
		return ListResult{Rule: e.deny, Scope: scope, Matched: matched}, true
	case e.deny != nil && weighted.Rule == nil:
		*weighted = ListResult{Rule: e.deny, Scope: scope, Matched: matched}
	}
	return ListResult{}, false
}

// parents yields host and each parent domain that still contains a dot:
// a.b.example.com, b.example.com, example.com.
func parents(host string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for d := host; strings.Contains(d, "."); {
			if !yield(d) {
				return
			}
			d = d[strings.IndexByte(d, '.')+1:]
		}
	}
}

// Exact checks one value against the exact rules and fingerprints of its kind. Layers are
// walked in precedence order; in each, an allow entry (allow rule or allow-labelled
// fingerprint) decides, then a hard deny rule, then a spam fingerprint (both known evidence).
// A weighted rule is kept but the walk goes on, so known evidence or an allow in a wider
// scope still decides.
func (v View) Exact(kind FingerprintKind, value string) ListResult {
	if value == "" {
		return ListResult{}
	}
	var weighted ListResult
	for _, sl := range v.layers {
		e, ok := sl.l.exact[kind][value]
		if !ok {
			continue
		}
		switch {
		case e.allow:
			return ListResult{Allowed: true, Scope: sl.scope, Matched: value}
		case e.deny != nil && e.deny.ActionClass == ActionHard:
			return ListResult{Rule: e.deny, Scope: sl.scope, Matched: value}
		case e.fp != nil:
			return ListResult{Fingerprint: e.fp, Scope: sl.scope, Matched: value}
		case e.deny != nil && weighted.Rule == nil:
			weighted = ListResult{Rule: e.deny, Scope: sl.scope, Matched: value}
		}
	}
	return weighted
}

// Blacklisted returns the manual blacklist entry for a number's keyed hash that applies in
// this group: the group's own entry first, then an instance-wide entry when the group uses
// the instance list (§9.8). It returns nil when the number isn't listed for this group.
func (v View) Blacklisted(pnHash string) *BlacklistEntry {
	if e := v.s.blGroup[v.o.Group][pnHash]; e != nil && v.o.Group != "" {
		return e
	}
	if v.o.UseInstanceBlacklist {
		return v.s.blInst[pnHash]
	}
	return nil
}
