//go:build integration

package store_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/corpus"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/textnorm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
)

const (
	grpA = "120363000000000001@g.us"
	grpB = "120363000000000002@g.us"
)

var hashKey = []byte("0123456789abcdef-integration")

func TestBlacklistStore(t *testing.T) {
	pool := storetest.Pool(t)
	ctx := t.Context()
	h, _ := blacklist.NewHasher(hashKey)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	admin := int64(7)
	add := func(scope rules.BlacklistScope, group, e164, reason string) (store.BlacklistEntry, bool, error) {
		return store.AddBlacklist(ctx, pool, store.BlacklistAdd{
			Scope: scope, GroupJID: group, E164: e164,
			Hash: h.Hash(e164), Reason: reason, AddedBy: &admin, Now: now,
		})
	}

	e1, inserted, err := add(rules.BlacklistGroup, grpA, "+966551234567", "fake visas")
	if err != nil || !inserted || e1.ID == 0 || e1.Hash != h.Hash("+966551234567") || e1.Reason != "fake visas" || *e1.AddedBy != admin {
		t.Fatalf("add = %+v, %v, %v; want a new entry with its keyed hash", e1, inserted, err)
	}
	again, inserted, err := add(rules.BlacklistGroup, grpA, "+966551234567", "other reason")
	if err != nil || inserted || again.ID != e1.ID || again.Reason != "fake visas" {
		t.Errorf("re-add = %+v, %v, %v; want the existing entry unchanged (idempotent)", again, inserted, err)
	}
	if _, inserted, err := add(rules.BlacklistGroup, grpB, "+966551234567", ""); err != nil || !inserted {
		t.Errorf("same number in another group: %v, %v; want a separate entry", inserted, err)
	}
	inst, inserted, err := add(rules.BlacklistInstance, "", "+966561234567", "operator")
	if err != nil || !inserted || inst.GroupJID != "" {
		t.Fatalf("instance add = %+v, %v, %v", inst, inserted, err)
	}
	if _, inserted, err := add(rules.BlacklistInstance, "", "+966561234567", ""); err != nil || inserted {
		t.Errorf("instance re-add = %v, %v; want idempotent (NULL group compares equal)", inserted, err)
	}
	for name, a := range map[string]store.BlacklistAdd{
		"not e164":         {Scope: rules.BlacklistGroup, GroupJID: grpA, E164: "0551234567", Hash: h.Hash("x"), Now: now},
		"bad hash":         {Scope: rules.BlacklistGroup, GroupJID: grpA, E164: "+966551234568", Hash: "XYZ", Now: now},
		"group, no group":  {Scope: rules.BlacklistGroup, E164: "+966551234568", Hash: h.Hash("+966551234568"), Now: now},
		"instance + group": {Scope: rules.BlacklistInstance, GroupJID: grpA, E164: "+966551234568", Hash: h.Hash("+966551234568"), Now: now},
	} {
		if _, _, err := store.AddBlacklist(ctx, pool, a); !errors.Is(err, store.ErrInvalidBlacklistEntry) {
			t.Errorf("%s: err = %v, want ErrInvalidBlacklistEntry", name, err)
		}
	}

	list, err := store.ListGroupBlacklist(ctx, pool, grpA)
	if err != nil || len(list) != 1 || list[0].E164 != "+966551234567" {
		t.Errorf("ListGroupBlacklist = %+v, %v; want group A's one entry", list, err)
	}
	check, err := store.CheckBlacklist(ctx, pool, grpA, "+966561234567")
	if err != nil || len(check) != 1 || check[0].Scope != rules.BlacklistInstance {
		t.Errorf("CheckBlacklist(instance number) = %+v, %v", check, err)
	}

	// The loader gives the in-memory snapshot hashes only, active entries only.
	loaded, err := store.RulesLoader{Q: pool}.LoadBlacklist(ctx)
	if err != nil || len(loaded) != 3 {
		t.Fatalf("LoadBlacklist = %+v, %v; want 3 active entries", loaded, err)
	}

	removed, err := store.RemoveBlacklist(ctx, pool, rules.BlacklistGroup, grpA, "+966551234567", &admin, now.Add(time.Hour))
	if err != nil || !removed {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	if removed, _ := store.RemoveBlacklist(ctx, pool, rules.BlacklistGroup, grpA, "+966551234567", nil, now); removed {
		t.Error("second remove = true, want false (not active)")
	}
	if list, _ := store.ListGroupBlacklist(ctx, pool, grpA); len(list) != 0 {
		t.Errorf("list after remove = %+v, want empty", list)
	}
	if again, inserted, err := add(rules.BlacklistGroup, grpA, "+966551234567", "back"); err != nil || !inserted || again.ID == e1.ID {
		t.Errorf("re-add after remove = %+v, %v, %v; want a new entry", again, inserted, err)
	}

	yes := true
	person := int64(99)
	if err := store.SetBlacklistIdentity(ctx, pool, inst.ID, &person, &yes); err != nil {
		t.Fatal(err)
	}
	if c, _ := store.CheckBlacklist(ctx, pool, grpB, "+966561234567"); len(c) != 1 || c[0].PersonID == nil || *c[0].PersonID != person || !*c[0].OnWhatsApp {
		t.Errorf("identity not linked: %+v", c)
	}
}

func TestRulesStoreAndNotify(t *testing.T) {
	pool := storetest.Pool(t)
	ctx := t.Context()
	h := rules.NewHolder(store.RulesLoader{Q: pool}, clock.Real{}, zerolog.Nop())
	changes := make(chan struct{}, 1)
	go func() { _ = store.ListenRulesChanged(ctx, pool, clock.Real{}, zerolog.Nop(), changes) }()
	go func() { _ = h.Run(ctx, changes) }()

	matches := func() bool {
		return len(h.Current().View(rules.ViewOptions{Group: grpA}).Keywords(rules.Text{Norm: "free bitcoin now"})) > 0
	}
	waitFor := func(want bool, why string) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for matches() != want {
			if time.Now().After(deadline) {
				t.Fatalf("after 1s: matches = %v, want %v (%s; F-041 wants changes live within a second)", !want, want, why)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	norm, _ := rules.NormalizePattern(rules.TypePhrase, "free bitcoin", nil)
	r, err := store.InsertRule(ctx, pool, rules.Rule{
		Scope: rules.ScopeGroup, ScopeID: grpA, Type: rules.TypePhrase,
		Pattern: "free bitcoin", NormPattern: norm, ActionClass: rules.ActionWeighted, Weight: 2, Enabled: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(true, "rule inserted")
	if ok, err := store.SetRuleEnabled(ctx, pool, r.ID, false); err != nil || !ok {
		t.Fatal(ok, err)
	}
	waitFor(false, "rule disabled")
	if ok, _ := store.SetRuleEnabled(ctx, pool, r.ID, true); !ok {
		t.Fatal("enable failed")
	}
	waitFor(true, "rule re-enabled")
	if ok, _ := store.DeleteRule(ctx, pool, r.ID); !ok {
		t.Fatal("delete failed")
	}
	waitFor(false, "rule deleted")

	bad := rules.Rule{
		Scope: rules.ScopeGroup, ScopeID: grpA, Type: rules.TypeRegex, Pattern: `((abc|def|ghi){30}){30}`,
		NormPattern: `((abc|def|ghi){30}){30}`, ActionClass: rules.ActionWeighted, Enabled: true,
	}
	if _, err := store.InsertRule(ctx, pool, bad, nil); !errors.Is(err, rules.ErrInvalidPattern) {
		t.Errorf("InsertRule(complex regex) err = %v, want rejected on save (F-043)", err)
	}

	fp := rules.Fingerprint{Kind: rules.FPInvite, Value: "InvCode", Label: rules.LabelSpam, Source: rules.SourceAuto, Scope: rules.ScopeGlobal}
	if err := store.UpsertFingerprint(ctx, pool, fp, time.Now()); err != nil {
		t.Fatal(err)
	}
	fp.Label = rules.LabelAllow
	if err := store.UpsertFingerprint(ctx, pool, fp, time.Now()); err != nil {
		t.Fatal(err)
	}
	fps, err := store.RulesLoader{Q: pool}.LoadFingerprints(ctx)
	if err != nil || len(fps) != 1 || fps[0].Label != rules.LabelAllow {
		t.Errorf("fingerprints = %+v, %v; want one, re-labelled allow", fps, err)
	}
	if ok, _ := store.DeleteFingerprint(ctx, pool, fp); !ok {
		t.Error("DeleteFingerprint = false")
	}
}

func TestHitCountersDoNotNotify(t *testing.T) {
	pool := storetest.Pool(t)
	ctx := t.Context()
	norm, _ := rules.NormalizePattern(rules.TypeKeyword, "casino", nil)
	r, err := store.InsertRule(ctx, pool, rules.Rule{
		Scope: rules.ScopeGlobal, Type: rules.TypeKeyword, Pattern: "casino",
		NormPattern: norm, ActionClass: rules.ActionHard, Enabled: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+store.RulesChannel); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE app.rules SET hits = hits + 1 WHERE id = $1", r.ID); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := contextWithTimeout(t, 300*time.Millisecond)
	defer cancel()
	if n, err := conn.Conn().WaitForNotification(wctx); err == nil {
		t.Errorf("hit counter update sent %+v, want no reload for counters", n)
	}
}

func TestCorpusStore(t *testing.T) {
	pool := storetest.Pool(t)
	ctx := t.Context()
	h, _ := blacklist.NewHasher(hashKey)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	e := &envelope.Envelope{
		Group: grpA, Texts: []envelope.TextSurface{{Source: "body", Raw: "Earn 500 dollars daily, WhatsApp 055 123 4567 https://evil.com/x"}},
		Media: []envelope.MediaRef{{Type: envelope.KindImage, SHA256: bytes.Repeat([]byte{9}, 32)}},
	}
	textnorm.Prepare(e, "SA")
	ent := corpus.FromMessage(e, corpus.Options{
		Scope: corpus.ScopeGroup, GroupJID: grpA, Status: corpus.StatusActive,
		Source: corpus.SourceReview, Region: "SA", Hasher: h, HashKey: hashKey, Confidence: 1, Now: now,
	})
	id, err := store.InsertCorpusEntry(ctx, pool, ent)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetCorpusEntry(ctx, pool, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.TextRawRedacted != ent.TextRawRedacted || *got.SimHash != *ent.SimHash || !bytes.Equal(got.MinHash, ent.MinHash) ||
		len(got.NumberHashes) != 1 || strings.Join(got.Domains, ",") != "evil.com" || len(got.MediaSHA256) != 1 {
		t.Errorf("round trip lost data:\n got %+v\nwant %+v", got, ent)
	}
	if strings.Contains(got.TextRawRedacted, "551234567") {
		t.Errorf("stored text has the number in the clear: %q", got.TextRawRedacted)
	}
	if _, err := pool.Exec(ctx, "UPDATE app.corpus_entries SET label = 'ham' WHERE id = $1", id); err == nil {
		t.Error("label 'ham' accepted, want the table to allow spam only (§7.5)")
	}

	live, err := store.ListLiveCorpusFingerprints(ctx, pool, now)
	if err != nil || len(live) != 1 || live[0].MinHash == nil || live[0].SimHash == nil {
		t.Fatalf("live fingerprints = %+v, %v", live, err)
	}
	if live, _ := store.ListLiveCorpusFingerprints(ctx, pool, now.Add(corpus.Retention+time.Hour)); len(live) != 0 {
		t.Errorf("expired entries listed: %+v", live)
	}

	admin := int64(3)
	key := corpus.PatternKeys(&ent)[0]
	if ok, err := store.ConfirmPattern(ctx, pool, key, grpA, &admin, id, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, _ := store.ConfirmPattern(ctx, pool, key, grpA, &admin, id, now); ok {
		t.Error("second confirmation in the same group counted, want one per group")
	}
	if ok, _ := store.ConfirmPattern(ctx, pool, key, grpB, nil, id, now.Add(time.Minute)); !ok {
		t.Error("confirmation in another group not counted")
	}
	if c, _ := store.ListPatternConfirmations(ctx, pool, key, now.Add(-30*24*time.Hour)); len(c) != 2 {
		t.Errorf("confirmations = %+v, want 2 groups", c)
	}

	norm, _ := rules.NormalizePattern(rules.TypeDomain, "evil.com", nil)
	learned, err := store.InsertRule(ctx, pool, rules.Rule{
		Scope: rules.ScopeGroup, ScopeID: grpA, Type: rules.TypeDomain,
		Pattern: "evil.com", NormPattern: norm, ActionClass: rules.ActionHard, Enabled: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.LinkLearnedRule(ctx, pool, learned.ID, id, grpA, now); err != nil {
		t.Fatal(err)
	}
	if ids, _ := store.ListLearnedRules(ctx, pool, id); len(ids) != 1 || ids[0] != learned.ID {
		t.Errorf("learned rules = %v", ids)
	}
	if ok, err := store.RetractCorpusEntry(ctx, pool, id); err != nil || !ok {
		t.Fatal(ok, err)
	}
	got, _ = store.GetCorpusEntry(ctx, pool, id)
	if got.Status != corpus.StatusRetracted || got.TextRawRedacted != "" || got.TextNorm != "" {
		t.Errorf("after retract: %+v, want retracted with no text (§7.5 overturns)", got)
	}
	if rs, _ := (store.RulesLoader{Q: pool}).LoadRules(ctx); len(rs) != 0 {
		t.Errorf("rules after retract = %+v, want the learned rule deleted", rs)
	}
	if ok, _ := store.SetCorpusStatus(ctx, pool, 999999, corpus.StatusActive); ok {
		t.Error("SetCorpusStatus on an unknown entry = true")
	}
	if err := store.RecordCorpusHit(ctx, pool, id, now); err != nil {
		t.Error(err)
	}
	if _, err := store.InsertCorpusEntry(ctx, pool, corpus.Entry{Scope: corpus.ScopeGroup}); !errors.Is(err, store.ErrInvalidCorpusEntry) {
		t.Errorf("entry without group: %v", err)
	}
}

func contextWithTimeout(t *testing.T, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(t.Context(), d)
}

func TestRetractKeepsRulesAnotherEntryTaught(t *testing.T) {
	pool := storetest.Pool(t)
	ctx := t.Context()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	newEntry := func() int64 {
		t.Helper()
		id, err := store.InsertCorpusEntry(ctx, pool, corpus.Entry{
			Kind: corpus.KindMessage, Scope: corpus.ScopeGroup,
			GroupJID: grpA, Status: corpus.StatusActive, Source: corpus.SourceReview, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b := newEntry(), newEntry()
	norm, _ := rules.NormalizePattern(rules.TypeDomain, "evil.com", nil)
	r, err := store.InsertRule(ctx, pool, rules.Rule{
		Scope: rules.ScopeGroup, ScopeID: grpA, Type: rules.TypeDomain,
		Pattern: "evil.com", NormPattern: norm, ActionClass: rules.ActionHard, Enabled: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{a, b} {
		if err := store.LinkLearnedRule(ctx, pool, r.ID, id, grpA, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.RetractCorpusEntry(ctx, pool, a); err != nil {
		t.Fatal(err)
	}
	if rs, _ := (store.RulesLoader{Q: pool}).LoadRules(ctx); len(rs) != 1 {
		t.Fatalf("rules after retracting one of two teachers = %d, want the shared rule kept", len(rs))
	}
	if _, err := store.RetractCorpusEntry(ctx, pool, b); err != nil {
		t.Fatal(err)
	}
	if rs, _ := (store.RulesLoader{Q: pool}).LoadRules(ctx); len(rs) != 0 {
		t.Errorf("rules after retracting both = %d, want none", len(rs))
	}
}
