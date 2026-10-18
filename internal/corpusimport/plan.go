package corpusimport

import (
	"cmp"
	"errors"
	"fmt"
	"hash/fnv"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/corpus"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/neardup"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/textnorm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// DefaultHoldout is the share of records held out for evaluation when the operator doesn't say.
const DefaultHoldout = 0.2

// MaxHoldout is the largest hold-out share accepted: the corpus is the point of an import.
const MaxHoldout = 0.5

// ErrOptions means the options can't be used.
var ErrOptions = errors.New("invalid import options")

// Scope is where imported entries go. The zero value is the whole instance (corpus scope
// global); otherwise one group.
type Scope struct {
	Group wa.JID
}

// Instance reports whether the scope is the whole instance.
func (s Scope) Instance() bool { return s.Group == "" }

func (s Scope) String() string {
	if s.Instance() {
		return "instance"
	}
	return "group:" + string(s.Group)
}

// ParseScope parses "instance" or "group:<jid>", where the JID is a group's (…@g.us).
func ParseScope(s string) (Scope, error) {
	s = strings.TrimSpace(s)
	if s == "instance" {
		return Scope{}, nil
	}
	g, ok := strings.CutPrefix(s, "group:")
	if !ok || !wa.JID(g).IsGroup() || strings.ContainsAny(g, " \t") || len(g) <= len("@g.us") {
		return Scope{}, fmt.Errorf("%w: scope %q: want instance or group:<group JID ending in @g.us>", ErrOptions, s)
	}
	return Scope{Group: wa.JID(g)}, nil
}

// Options are the import flags.
type Options struct {
	Scope Scope
	// Holdout is the share of spam records held out for evaluation, split by cluster
	// (0 ≤ Holdout ≤ MaxHoldout). Ignored when HoldoutClusters is set.
	Holdout float64
	// HoldoutClusters, when set, are the cluster_ids to hold out instead of a random split.
	HoldoutClusters []string
	DryRun          bool
	// AllowHam adds the ham records' near-duplicate fingerprints (never their text) to the
	// group's allow-list. Group scope only: the allow-list is per group.
	AllowHam bool
	// HashKey is the deployment's number-hash key (HASH_KEY).
	HashKey    []byte
	FileName   string
	FileSHA256 string // hex, of the whole input
	Format     string // FormatJSONL or FormatCSV
	Now        time.Time
}

// Validate checks the options.
func (o Options) Validate() error {
	switch {
	case len(o.HashKey) == 0:
		return fmt.Errorf("%w: HASH_KEY is required, so advertised numbers are stored as keyed hashes", ErrOptions)
	case math.IsNaN(o.Holdout) || o.Holdout < 0 || o.Holdout > MaxHoldout:
		return fmt.Errorf("%w: --holdout must be between 0 and %.1f", ErrOptions, MaxHoldout)
	case o.AllowHam && o.Scope.Instance():
		return fmt.Errorf("%w: --allow-ham needs a group scope (allow-lists are per group)", ErrOptions)
	case o.Now.IsZero():
		return fmt.Errorf("%w: no time", ErrOptions)
	}
	return nil
}

// Item is one record after normalization and fingerprinting: for spam, the corpus entry it
// becomes (redacted text, fingerprints, hashed numbers); for ham, only what the in-memory checks
// need.
type Item struct {
	Record   Record
	Envelope *envelope.Envelope // the record as a message, for the replay
	Entry    corpus.Entry
	FP       neardup.Fingerprint // of the entry's normalized text, as the corpus index computes it
	Kind     string              // the record's kind, or the one inferred from its content
	Language string              // the record's language, or the one detected from its text
	Cluster  string              // the split cluster's key
	Merged   int                 // duplicates of this record merged into it
}

// Plan is what an import will do.
type Plan struct {
	Corpus  []Item // spam to store as corpus entries
	Holdout []Item // spam held out for evaluation
	Ham     []Item // in memory only
	// MergedInFile counts spam records merged into an earlier near-identical record of the file.
	MergedInFile int
	// MergedExisting counts records dropped because the live corpus already has them (DropExisting).
	MergedExisting  int
	Clusters        int
	HoldoutClusters int
	// Thin lists the categories and languages of the file's spam with fewer than MinExamples
	// examples, counted before DropExisting.
	Thin []string
}

// NewPlan normalizes and fingerprints recs, merges exact and near-identical spam (the corpus
// index's CERTAIN band), groups spam into clusters and splits the hold-out (DESIGN.md §7.8 steps
// 2–4). Merged copies are stored or held out once. Clusters follow cluster_id where given, and
// otherwise related records (see cluster), so a near-copy of a hold-out record can't end up in
// the corpus.
func NewPlan(recs []Record, o Options) (Plan, error) {
	if err := o.Validate(); err != nil {
		return Plan{}, err
	}
	hasher, err := blacklist.NewHasher(o.HashKey)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: number hash key: %w", ErrOptions, err)
	}
	var p Plan
	var spam []Item
	for _, r := range recs {
		it := prepare(r, o, hasher)
		if r.Label == LabelHam {
			p.Ham = append(p.Ham, it)
			continue
		}
		spam = append(spam, it)
	}

	// Merge exact and near-identical copies into the first one: they're stored once, or held
	// out once.
	var items []Item
	byKey := map[string]int{}
	ix := neardup.NewIndex(neardup.DefaultThresholds())
	for _, it := range spam {
		key := exactKey(&it)
		if j, ok := byKey[key]; ok {
			mergeInto(&items[j], &it)
			p.MergedInFile++
			continue
		}
		if !it.FP.Empty() {
			if m := ix.Match(it.FP); m.Closeness == neardup.Certain {
				j, _ := strconv.Atoi(m.EntryID)
				mergeInto(&items[j], &it)
				p.MergedInFile++
				continue
			}
			ix.Add(strconv.Itoa(len(items)), it.FP)
		}
		byKey[key] = len(items)
		items = append(items, it)
	}
	cluster(items)
	p.Thin = thinOf(items)
	held := split(items, o)
	clusters := map[string]bool{}
	for _, it := range items {
		clusters[it.Cluster] = true
		if held[it.Cluster] {
			p.Holdout = append(p.Holdout, it)
		} else {
			p.Corpus = append(p.Corpus, it)
		}
	}
	p.Clusters = len(clusters)
	p.countHeldClusters()
	return p, nil
}

// cluster sets each item's Cluster, the unit the split never divides. Records with the same
// cluster_id share one; two records that both name a cluster_id are never linked otherwise, as
// the operator's ids are authoritative. A record without one is linked to every record it is
// related to, loose near-duplicates included, so no reworded copy can sit on the other side of
// the split; if that links two named clusters, they become one unit.
func cluster(items []Item) {
	uf := newUnionFind(len(items))
	first := map[string]int{}
	for i, it := range items {
		if c := it.Record.ClusterID; c != "" {
			if j, ok := first[c]; ok {
				uf.union(i, j)
			} else {
				first[c] = i
			}
		}
	}
	t := neardup.DefaultThresholds()
	for i, a := range items {
		if a.Record.ClusterID != "" || a.FP.Empty() {
			continue
		}
		for j, b := range items {
			if j == i || b.FP.Empty() {
				continue
			}
			if neardup.Distance(a.FP, b.FP) <= t.LooseSimHashMaxDistance || neardup.Jaccard(a.FP, b.FP) >= t.LooseMinHashMinJaccard {
				uf.union(i, j)
			}
		}
	}
	names := map[int]string{}
	for i, it := range items {
		root := uf.find(i)
		if c := it.Record.ClusterID; c != "" && (names[root] == "" || "id:"+c < names[root]) {
			names[root] = "id:" + c
		}
	}
	for i := range items {
		root := uf.find(i)
		items[i].Cluster = cmp.Or(names[root], fmt.Sprintf("auto:%d", root))
	}
}

func (p *Plan) countHeldClusters() {
	held := map[string]bool{}
	for _, it := range p.Holdout {
		held[it.Cluster] = true
	}
	p.HoldoutClusters = len(held)
}

// Existing is a stored corpus entry or hold-out row an import dedupes against.
type Existing struct {
	TextNorm    string
	MediaSHA256 [][]byte
	PHashes     []int64
}

// DropExisting drops the items already stored: the same normalized text as an existing entry or
// hold-out row, a near-identical one (CERTAIN band), or, for an item without text, only media
// and pHashes that are all stored already. Dropped items are neither stored nor held out, so
// re-importing a file stores nothing new and never turns hold-out records into corpus entries.
// It returns how many items were dropped (their merged copies stay counted in MergedInFile).
func (p *Plan) DropExisting(existing []Existing) int {
	ix := neardup.NewIndex(neardup.DefaultThresholds())
	keys := map[string]bool{}
	media := map[string]bool{}
	phashes := map[int64]bool{}
	for i, e := range existing {
		if e.TextNorm != "" {
			keys["t:"+neardup.Key(e.TextNorm)] = true
			ix.Add(strconv.Itoa(i), neardup.Compute(neardup.Key(e.TextNorm)))
		}
		for _, m := range e.MediaSHA256 {
			media[string(m)] = true
		}
		for _, h := range e.PHashes {
			phashes[h] = true
		}
	}
	dropped := 0
	known := func(it Item) bool {
		dup := false
		switch {
		case it.Entry.TextNorm != "":
			dup = keys["t:"+neardup.Key(it.Entry.TextNorm)] || (!it.FP.Empty() && ix.Match(it.FP).Closeness == neardup.Certain)
		case len(it.Entry.MediaSHA256) > 0 || len(it.Entry.PHashes) > 0:
			dup = !slices.ContainsFunc(it.Entry.MediaSHA256, func(m []byte) bool { return !media[string(m)] }) &&
				!slices.ContainsFunc(it.Entry.PHashes, func(h int64) bool { return !phashes[h] })
		}
		if dup {
			// Its merged copies are already counted in MergedInFile.
			dropped++
		}
		return dup
	}
	p.Corpus = slices.DeleteFunc(p.Corpus, known)
	p.Holdout = slices.DeleteFunc(p.Holdout, known)
	p.countHeldClusters()
	p.MergedExisting += dropped
	return dropped
}

// prepare normalizes and fingerprints one record the way a confirmed message is: textnorm, then
// corpus.FromMessage, which redacts the text and hashes advertised numbers with the key.
func prepare(r Record, o Options, h blacklist.Hasher) Item {
	env := r.envelope()
	textnorm.Prepare(env, "")
	scope, group := corpus.ScopeGlobal, ""
	if !o.Scope.Instance() {
		scope, group = corpus.ScopeGroup, string(o.Scope.Group)
	}
	e := corpus.FromMessage(env, corpus.Options{
		Scope: scope, GroupJID: group, Status: corpus.StatusActive, Source: corpus.SourceImport,
		Hasher: h, HashKey: o.HashKey, Confidence: 1, Now: o.Now,
	})
	if r.PHash != "" {
		e.PHashes = append(e.PHashes, int64(beUint64(decodeHex(r.PHash))))
	}
	it := Item{Record: r, Envelope: env, Entry: e, FP: neardup.Compute(neardup.Key(e.TextNorm))}
	it.Kind = cmp.Or(r.Kind, inferKind(r, e))
	it.Language = cmp.Or(r.Language, DetectLanguage(r.Text))
	return it
}

// envelope is the record as a message: its text, its links and its media hashes.
func (r Record) envelope() *envelope.Envelope {
	e := &envelope.Envelope{Kind: envelope.KindText}
	if r.Text != "" {
		e.Texts = []envelope.TextSurface{{Source: "text", Raw: r.Text}}
	}
	for _, u := range r.URLs {
		e.URLs = append(e.URLs, envelope.URLRef{URL: withScheme(u), Source: "import"})
	}
	mk, isMedia := mediaKind(r.Kind)
	for _, m := range r.MediaSHA256 {
		e.Media = append(e.Media, envelope.MediaRef{Type: mk, SHA256: decodeHex(m)})
	}
	// A record of another kind (a contact card, a link) keeps the text kind: the replay matches
	// its content, not a policy block for a type the record only approximates.
	if len(e.Media) > 0 && (r.Kind == "" || isMedia) {
		e.Kind = mk
	}
	return e
}

// mediaKind is the envelope kind of a record's media: its own kind for a media kind, an image
// otherwise.
func mediaKind(kind string) (envelope.Kind, bool) {
	switch kind {
	case "image", "video", "sticker", "document", "audio", "voice":
		return envelope.Kind(kind), true
	}
	return envelope.KindImage, false
}

// inferKind names a record's kind from what it carries: a group invite, a channel link, media,
// another link, or text.
func inferKind(r Record, e corpus.Entry) string {
	switch {
	case len(e.InviteCodes) > 0:
		return "invite"
	case len(e.ChannelJIDs) > 0:
		return "channel_share"
	case len(r.MediaSHA256) > 0 || r.PHash != "":
		return "image"
	case len(e.URLs) > 0 || len(e.Domains) > 0:
		return "link"
	}
	return "text"
}

// DetectLanguage guesses a text's language from the script most of its letters are in: ar for
// Arabic script, en for Latin, ru for Cyrillic, und (undetermined) otherwise. It's a rough
// guess for the report's per-language counts, nothing more.
func DetectLanguage(text string) string {
	var arabic, latin, cyrillic, letters int
	for _, r := range text {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		switch {
		case unicode.Is(unicode.Arabic, r):
			arabic++
		case unicode.Is(unicode.Latin, r):
			latin++
		case unicode.Is(unicode.Cyrillic, r):
			cyrillic++
		}
	}
	switch {
	case letters == 0:
		return "und"
	case arabic*2 > letters:
		return "ar"
	case latin*2 > letters:
		return "en"
	case cyrillic*2 > letters:
		return "ru"
	}
	return "und"
}

// exactKey identifies an exact duplicate: the same matching text, or, without text, the same
// media.
func exactKey(it *Item) string {
	if it.Entry.TextNorm != "" {
		return "t:" + neardup.Key(it.Entry.TextNorm)
	}
	m := slices.Clone(it.Record.MediaSHA256)
	slices.Sort(m)
	return "m:" + strings.Join(m, ",") + "|" + it.Record.PHash
}

// mergeInto folds a duplicate into the kept record: its links, numbers and media join the
// entry, so nothing a variant adds is lost.
func mergeInto(dst *Item, src *Item) {
	dst.Merged += 1 + src.Merged
	e, s := &dst.Entry, &src.Entry
	e.URLs = union(e.URLs, s.URLs)
	e.Domains = union(e.Domains, s.Domains)
	e.ChannelJIDs = union(e.ChannelJIDs, s.ChannelJIDs)
	e.InviteCodes = union(e.InviteCodes, s.InviteCodes)
	e.NumberHashes = unionBytes(e.NumberHashes, s.NumberHashes)
	e.MediaSHA256 = unionBytes(e.MediaSHA256, s.MediaSHA256)
	for _, ph := range s.PHashes {
		if !slices.Contains(e.PHashes, ph) {
			e.PHashes = append(e.PHashes, ph)
		}
	}
}

func union(a, b []string) []string {
	for _, v := range b {
		if !slices.Contains(a, v) {
			a = append(a, v)
		}
	}
	return a
}

func unionBytes(a, b [][]byte) [][]byte {
	for _, v := range b {
		if !slices.ContainsFunc(a, func(x []byte) bool { return string(x) == string(v) }) {
			a = append(a, v)
		}
	}
	return a
}

func beUint64(b []byte) uint64 {
	var n uint64
	for _, c := range b {
		n = n<<8 | uint64(c)
	}
	return n
}

// split picks the clusters to hold out. With HoldoutClusters it takes exactly those, instead of
// a random split. Otherwise it holds out round(Holdout × clusters) clusters, shared out over the
// strata (category and kind of a cluster's first record) by largest remainder, no stratum giving
// more than ceil(Holdout × its clusters); each stratum gives its first clusters in a fixed
// pseudo-random order.
func split(items []Item, o Options) map[string]bool {
	held := map[string]bool{}
	if len(o.HoldoutClusters) > 0 {
		want := map[string]bool{}
		for _, c := range o.HoldoutClusters {
			if c = strings.TrimSpace(c); c != "" {
				want[c] = true
			}
		}
		// A cluster is held out when any of its records names a listed cluster_id, so linked
		// near-duplicates follow it.
		for _, it := range items {
			if want[it.Record.ClusterID] {
				held[it.Cluster] = true
			}
		}
		return held
	}
	if o.Holdout <= 0 {
		return held
	}
	strata := map[string][]string{}
	seen := map[string]bool{}
	total := 0
	for _, it := range items {
		if seen[it.Cluster] {
			continue
		}
		seen[it.Cluster] = true
		s := it.Record.Category + "\x00" + it.Kind
		strata[s] = append(strata[s], it.Cluster)
		total++
	}
	type share struct {
		stratum   string
		quota     int
		cap       int
		remainder float64
	}
	var shares []*share
	assigned := 0
	for _, s := range slices.Sorted(maps.Keys(strata)) {
		exact := o.Holdout * float64(len(strata[s]))
		q := int(math.Floor(exact))
		shares = append(shares, &share{stratum: s, quota: q, cap: int(math.Ceil(exact)), remainder: exact - float64(q)})
		assigned += q
	}
	byRemainder := slices.Clone(shares)
	slices.SortStableFunc(byRemainder, func(a, b *share) int { return cmp.Compare(b.remainder, a.remainder) })
	target := int(math.Round(o.Holdout * float64(total)))
	for _, sh := range byRemainder {
		if assigned >= target {
			break
		}
		if sh.quota < sh.cap {
			sh.quota++
			assigned++
		}
	}
	for _, sh := range shares {
		cs := strata[sh.stratum]
		slices.SortFunc(cs, func(a, b string) int { return cmp.Or(cmp.Compare(order(a), order(b)), cmp.Compare(a, b)) })
		for _, c := range cs[:sh.quota] {
			held[c] = true
		}
	}
	return held
}

// thinOf names the categories and languages with fewer than MinExamples spam examples.
func thinOf(items []Item) []string {
	cats, langs := map[string]int{}, map[string]int{}
	for _, it := range items {
		if it.Record.Category != "" {
			cats[it.Record.Category] += 1 + it.Merged
		}
		langs[it.Language] += 1 + it.Merged
	}
	return append(thin("category", cats), thin("language", langs)...)
}

// order is a cluster's fixed place in the split order, so re-running an import splits the same.
func order(key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return h.Sum64()
}

type unionFind []int

func newUnionFind(n int) unionFind {
	u := make(unionFind, n)
	for i := range u {
		u[i] = i
	}
	return u
}

func (u unionFind) find(i int) int {
	for u[i] != i {
		u[i] = u[u[i]]
		i = u[i]
	}
	return i
}

func (u unionFind) union(a, b int) {
	ra, rb := u.find(a), u.find(b)
	if ra != rb {
		u[max(ra, rb)] = min(ra, rb)
	}
}
