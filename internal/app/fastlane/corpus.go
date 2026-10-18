package fastlane

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/corpus"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/neardup"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// CorpusRefresh is how often the corpus index reloads: corpus_entries changes (a `.spam`, a
// review ✅, an overturn) don't send a NOTIFY, so new confirmations reach matching within this.
const CorpusRefresh = 30 * time.Second

// Corpus is the confirmed-spam near-duplicate index (F-117) over corpus_entries (F-113): each
// group's own active entries plus promoted global ones, and each group's allow-list of overturned
// fingerprints. Fingerprints are recomputed from the entries' stored (redacted) normalized text
// with neardup's matching key; the analyzer builds the same text for a live message
// (CorpusText), so the two compare like for like, homoglyphs and phone numbers included.
// Provisional, retracted and expired entries never match.
type Corpus struct {
	pool *pgxpool.Pool
	clk  clock.Clock
	log  zerolog.Logger

	mu     sync.RWMutex
	global *neardup.Index
	groups map[wa.JID]*neardup.Index
}

// NewCorpus returns an empty index; call Reload (or Run) to fill it.
func NewCorpus(pool *pgxpool.Pool, clk clock.Clock, log zerolog.Logger) *Corpus {
	return &Corpus{pool: pool, clk: clk, log: log, global: neardup.NewIndex(neardup.DefaultThresholds()), groups: map[wa.JID]*neardup.Index{}}
}

// Indexes implements neardup.Indexes: the group's own entries, then the global ones.
func (c *Corpus) Indexes(group wa.JID) []*neardup.Index {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return []*neardup.Index{c.groups[group], c.global}
}

// Reload rebuilds the index from the live corpus entries and the allow-list, and swaps it in.
func (c *Corpus) Reload(ctx context.Context) error {
	q := db.New(c.pool)
	rows, err := q.ListMatchableCorpusTexts(ctx, c.clk.Now())
	if err != nil {
		return fmt.Errorf("corpus: list entries: %w", err)
	}
	allows, err := q.ListCorpusAllow(ctx)
	if err != nil {
		return fmt.Errorf("corpus: list allow-list: %w", err)
	}
	global := neardup.NewIndex(neardup.DefaultThresholds())
	groups := map[wa.JID]*neardup.Index{}
	group := func(g wa.JID) *neardup.Index {
		if groups[g] == nil {
			groups[g] = neardup.NewIndex(neardup.DefaultThresholds())
		}
		return groups[g]
	}
	for _, r := range rows {
		fp := neardup.Compute(neardup.Key(r.TextNorm))
		if fp.Empty() {
			continue
		}
		id := fmt.Sprint(r.ID)
		switch {
		case corpus.Scope(r.Scope) == corpus.ScopeGlobal || corpus.Status(r.Status) == corpus.StatusPromoted:
			global.Add(id, fp)
		case r.GroupJid.Valid && r.GroupJid.String != "":
			group(wa.JID(r.GroupJid.String)).Add(id, fp)
		}
	}
	for _, a := range allows {
		mh, err := neardup.MinHashFromBytes(a.Minhash)
		if err != nil {
			c.log.Warn().Err(err).Str("group", a.GroupJid).Msg("skipping a corrupt allow-list fingerprint")
			continue
		}
		// The group's index carries its allow-list; the analyzer checks it before every index,
		// the global one included.
		group(wa.JID(a.GroupJid)).Allow(neardup.Fingerprint{SimHash: uint64(a.Simhash), MinHash: mh, Tokens: int(a.Tokens)})
	}
	c.mu.Lock()
	c.global, c.groups = global, groups
	c.mu.Unlock()
	return nil
}

// Run reloads every CorpusRefresh until ctx ends. A failed reload keeps the previous index.
func (c *Corpus) Run(ctx context.Context) error {
	for {
		if err := c.Reload(ctx); err != nil && ctx.Err() == nil {
			c.log.Warn().Err(err).Msg("corpus reload failed; keeping the previous index")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-c.clk.After(CorpusRefresh):
		}
	}
}
