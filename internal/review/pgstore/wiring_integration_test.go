//go:build integration

package pgstore_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	dblacklist "github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist/blacklisttest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// people is an in-memory PersonMap.
type people struct {
	mu   sync.Mutex
	ids  map[wa.JID]int64
	jids map[int64]wa.JID
}

func (p *people) PersonID(_ context.Context, j wa.JID) (int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ids == nil {
		p.ids, p.jids = map[wa.JID]int64{}, map[int64]wa.JID{}
	}
	if id, ok := p.ids[j]; ok {
		return id, nil
	}
	id := int64(len(p.ids) + 1)
	p.ids[j], p.jids[id] = id, j
	return id, nil
}

func (p *people) JID(_ context.Context, id int64) (wa.JID, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.jids[id], nil
}

var testKey = []byte("test-key-0123456789abcdef")

func hasher(t *testing.T) dblacklist.Hasher {
	t.Helper()
	h, err := dblacklist.NewHasher(testKey)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// TestBlacklistOnNumberBlacklist runs the blacklist service on WP-G's number_blacklist table:
// add, list, check, resolve and remove, with the keyed hash content matching relies on.
func TestBlacklistOnNumberBlacklist(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	ppl := &people{}
	h := hasher(t)
	list := pgstore.BlacklistList{DB: pool, HashKey: testKey, Persons: ppl, Clock: clock.NewFake(start)}
	groups := &blacklisttest.Groups{}
	groups.SetMembers(group)
	svc := blacklist.NewService(blacklist.Deps{
		List: list, Normalize: pgstore.PhoneNormalizer{}, Groups: groups, Actions: &reviewtest.Actions{},
		Settings: &blacklisttest.Settings{Default: blacklist.GroupSettings{UseInstance: true, Mode: review.ModeAuto, DefaultCountry: "SA"}},
		Clock:    clock.NewFake(start),
	})
	const adder wa.JID = "300000000000001@lid"
	res, err := svc.Add(ctx, blacklist.AddRequest{Group: group, Numbers: []string{"0551234567", "+966 55 765 4321", "12"}, Reason: "scam", By: adder})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 2 || len(res.Invalid) != 1 || res.Added[0].E164 != "+966551234567" {
		t.Fatalf("add = %+v, want two numbers in E.164 and one invalid", res)
	}
	es, err := svc.ForGroup(ctx, group)
	if err != nil || len(es) != 2 || es[0].E164 != "+966551234567" || es[0].AddedBy != adder || es[0].Reason != "scam" {
		t.Fatalf("ForGroup = %+v, %v; want both entries oldest first with adder and reason", es, err)
	}
	var stored string
	if err := pool.QueryRow(ctx, "SELECT pn_hash FROM app.number_blacklist WHERE pn_e164 = '+966551234567'").Scan(&stored); err != nil || stored != h.Hash("+966551234567") {
		t.Errorf("stored hash %q, %v; want the deployment's keyed hash so content matching finds it (F-127)", stored, err)
	}
	if err := list.SetResolved(ctx, "+966551234567", spamLID, true); err != nil {
		t.Fatal(err)
	}
	if es, _ := list.ByPerson(ctx, spamLID); len(es) != 1 || es[0].Person != spamLID || es[0].OnWhatsApp == nil || !*es[0].OnWhatsApp {
		t.Errorf("ByPerson after resolution = %+v, want the entry linked to the LID", es)
	}
	if _, err := svc.Add(ctx, blacklist.AddRequest{Numbers: []string{"+12025550147"}, By: op, Operator: true, Instance: true}); err != nil {
		t.Fatal(err)
	}
	if es, _ := list.Instance(ctx); len(es) != 1 || es[0].Scope != blacklist.ScopeInstance {
		t.Errorf("Instance = %+v, want the instance entry", es)
	}
	if chk, err := svc.Check(ctx, group, "+12025550147"); err != nil || chk.Instance == nil {
		t.Errorf("Check(instance number) = %+v, %v; want the instance entry", chk, err)
	}
	rm, err := svc.Remove(ctx, blacklist.RemoveRequest{Group: group, Numbers: []string{"0551234567", "0557777777"}, By: adder})
	if err != nil || len(rm.Removed) != 1 || len(rm.NotFound) != 1 {
		t.Fatalf("remove = %+v, %v; want one removed and one not listed", rm, err)
	}
	if es, _ := list.ByNumber(ctx, "+966551234567"); len(es) != 0 {
		t.Errorf("ByNumber after remove = %+v, want none active", es)
	}
}

const (
	spamLID wa.JID = "200000000000009@lid"
	op      wa.JID = "300000000000099@lid"
)

// TestCorpusConfirmAndOverturn runs learning on WP-G's corpus tables: a confirmation becomes an
// active entry with its patterns; an overturn retracts it, allow-lists its SimHash and leaves
// no text (DESIGN §7.5).
func TestCorpusConfirmAndOverturn(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	c := pgstore.Corpus{Pool: pool, HashKey: testKey, Persons: &people{}, Clock: clock.NewFake(start)}
	ev := pgstore.Evidence{DB: pool}
	l := review.NewLearning(review.LearningDeps{Evidence: ev, Corpus: c, Standing: &reviewtest.Standing{}, Clock: clock.NewFake(start)})
	msg := wa.MessageRef{Sender: spamLID, ID: "SPAMX"}
	text := "Earn 3000 riyal daily!! join t.me/richfast and message +966 55 999 1234 now"
	if err := l.Snapshot(ctx, review.Snapshot{Group: group, Message: msg, Text: text}); err != nil {
		t.Fatal(err)
	}
	if err := l.ConfirmSpam(ctx, review.Confirmation{Group: group, Message: msg, Text: text, Admin: "300000000000001@lid", Source: "command"}); err != nil {
		t.Fatal(err)
	}
	id, ok, err := c.EntryFor(ctx, group, "SPAMX")
	if err != nil || !ok {
		t.Fatalf("EntryFor = %v, %v; want the confirmed entry", ok, err)
	}
	e, err := store.GetCorpusEntry(ctx, pool, id)
	if err != nil || e.SimHash == nil || strings.Contains(e.TextRawRedacted, "999 1234") || len(e.NumberHashes) == 0 {
		t.Fatalf("entry = %+v, %v; want a SimHash, the number only as a keyed hash", e, err)
	}
	var confirmations int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM app.corpus_confirmations WHERE corpus_entry_id = $1", id).Scan(&confirmations); err != nil || confirmations == 0 {
		t.Errorf("pattern confirmations = %d, %v; want the entry's patterns recorded for promotion", confirmations, err)
	}

	if err := l.Overturn(ctx, review.Overturn{Group: group, Message: msg, Actor: "300000000000001@lid"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := c.EntryFor(ctx, group, "SPAMX"); ok {
		t.Error("entry still live after the overturn, want it retracted")
	}
	e, _ = store.GetCorpusEntry(ctx, pool, id)
	if e.Status != "retracted" || e.TextRawRedacted != "" || e.TextNorm != "" {
		t.Errorf("retracted entry = status %s text %q norm %q, want retracted with no text", e.Status, e.TextRawRedacted, e.TextNorm)
	}
	if s, err := ev.Get(ctx, group, "SPAMX"); err != nil || s.Text != "" {
		t.Errorf("snapshot text after overturn = %q, %v; want deleted", s.Text, err)
	}
	var label string
	if err := pool.QueryRow(ctx, "SELECT label FROM app.fingerprints WHERE kind = 'simhash' AND scope = 'group' AND scope_id = $1", string(group)).Scan(&label); err != nil || label != string(rules.LabelAllow) {
		t.Errorf("fingerprint allow-list = %q, %v; want the SimHash allow-listed in the group", label, err)
	}

	// No text and no entry: nothing to fingerprint, so nothing is allow-listed.
	if err := c.AllowFingerprint(ctx, review.FingerprintAllow{Group: rgroup, Message: wa.MessageRef{ID: "EMPTY"}}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM app.fingerprints WHERE scope_id = $1", string(rgroup)).Scan(&n); err != nil || n != 0 {
		t.Errorf("allow-list from empty text stored %d fingerprints, want 0", n)
	}
}

// TestListsOnRules: .domain and .word edit group rules; allowing a blocked domain unblocks it.
func TestListsOnRules(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	l := pgstore.Lists{DB: pool, Persons: &people{}}
	const actor wa.JID = "300000000000001@lid"
	count := func(q string, args ...any) int {
		var n int
		if err := pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := l.BlockDomain(ctx, group, "https://www.Bad.example/x", actor); err != nil {
		t.Fatal(err)
	}
	if err := l.BlockDomain(ctx, group, "bad.example", actor); err != nil {
		t.Fatal(err)
	}
	if n := count("SELECT count(*) FROM app.rules WHERE type='domain' AND action_class='hard' AND scope_id=$1", string(group)); n != 1 {
		t.Errorf("block twice: %d block rules, want 1", n)
	}
	if err := l.AllowDomain(ctx, group, "bad.example", actor); err != nil {
		t.Fatal(err)
	}
	if n := count("SELECT count(*) FROM app.rules WHERE type='domain' AND action_class='hard' AND scope_id=$1", string(group)); n != 0 {
		t.Error("allowing a blocked domain kept the block")
	}
	if n := count("SELECT count(*) FROM app.rules WHERE type='domain' AND action_class='allow' AND scope_id=$1", string(group)); n != 1 {
		t.Error("allow rule missing")
	}
	if err := l.BlockWord(ctx, group, "Free  Money", actor); err != nil {
		t.Fatal(err)
	}
	if n := count("SELECT count(*) FROM app.rules WHERE type='phrase' AND scope_id=$1", string(group)); n != 1 {
		t.Error("phrase rule missing")
	}
	if err := l.UnblockWord(ctx, group, "free money", actor); err != nil {
		t.Fatal(err)
	}
	if n := count("SELECT count(*) FROM app.rules WHERE type='phrase' AND scope_id=$1", string(group)); n != 0 {
		t.Error("unblock kept the phrase rule")
	}
	if _, err := (pgstore.PhoneNormalizer{}).Normalize("not a number", "SA"); !errors.Is(err, blacklist.ErrInvalid) {
		t.Errorf("PhoneNormalizer(invalid) = %v, want ErrInvalid", err)
	}
}
