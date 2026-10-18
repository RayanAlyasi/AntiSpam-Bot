//go:build integration

package fastlane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/proto"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/corpus"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/neardup"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/inbox"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter/inboxenv"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter/synth"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

const (
	bot       wa.JID = "100@lid"
	protected wa.JID = "120363000000000001@g.us"
	other     wa.JID = "120363000000000002@g.us"
	spam             = "Congratulations you have been selected for our exclusive crypto investment program earn five hundred dollars every single day from home"
)

// TestLaneShadowGroupRecordsAndNeverActs assembles the production fast lane on Postgres: a group
// with saved settings (here shadow mode, as an admin's `.mode shadow` would store) is protected,
// known spam there is recorded as a shadow detection and nothing reaches WhatsApp; a group with
// no settings isn't moderated at all.
func TestLaneShadowGroupRecordsAndNeverActs(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	clk := clock.NewFake(time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC))
	client := fake.New()
	corpus := neardup.NewIndex(neardup.DefaultThresholds())
	corpus.Add("c1", neardup.FingerprintText(spam))
	lane, err := New(ctx, Deps{Pool: pool, Clock: clk, Bot: bot, Client: client, Log: zerolog.Nop(), HashKey: []byte("fastlane-test-number-key-000001"),
		Corpus: func(wa.JID) []*neardup.Index { return []*neardup.Index{corpus} }})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := lane.Settings.Set(ctx, protected, "200@lid", "mode", json.RawMessage(`"shadow"`)); err != nil {
		t.Fatal(err)
	}

	for _, chat := range []wa.JID{protected, other} {
		e, err := synth.Envelope(synth.Spec{Chat: chat, Sender: "300@lid", ID: "M1", Time: clk.Now(), Text: spam}, bot)
		if err != nil {
			t.Fatal(err)
		}
		v, err := lane.Process(ctx, e, clk.Now())
		if err != nil {
			t.Fatal(err)
		}
		switch chat {
		case protected:
			if v.Mode != pipeline.ModeShadow || v.Band != pipeline.BandCertain || len(v.Actions) == 0 {
				t.Fatalf("protected shadow group verdict %+v, want certain with would-be actions", v)
			}
		case other:
			if v.Route != pipeline.RouteNone || len(v.Actions) != 0 {
				t.Fatalf("unprotected group verdict %+v, want nothing", v)
			}
		}
	}
	rows, err := store.NewPolicyStore(pool).ListDetections(ctx, protected, 0, 10)
	if err != nil || len(rows) != 1 || rows[0].Mode != "shadow" {
		t.Fatalf("detections %+v, %v; want one shadow detection", rows, err)
	}
	if rows, _ := store.NewPolicyStore(pool).ListDetections(ctx, other, 0, 10); len(rows) != 0 {
		t.Fatalf("the unprotected group got %d detections, want none", len(rows))
	}
	if calls := client.Calls(); len(calls) != 0 {
		t.Fatalf("WhatsApp saw %d calls, want none: shadow mode acts on nothing (DESIGN §8.4)", len(calls))
	}
}

// TestLaneReloadsBlacklistOnNotify adds a number to the blacklist in Postgres while the lane
// runs: the rules_changed notification reloads the snapshot, and a contact card with that number
// becomes known spam without a restart (DESIGN.md §9.8, F-127).
func TestLaneReloadsBlacklistOnNotify(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := storetest.Pool(t)
	clk := clock.NewFake(time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC))
	key := []byte("fastlane-test-number-key-000001")
	lane, err := New(ctx, Deps{Pool: pool, Clock: clk, Bot: bot, Client: fake.New(), Log: zerolog.Nop(), HashKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := lane.Settings.Set(ctx, protected, "200@lid", "mode", json.RawMessage(`"shadow"`)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- lane.Run(ctx) }()

	hasher, _ := blacklist.NewHasher(key)
	if _, _, err := store.AddBlacklist(ctx, pool, store.BlacklistAdd{Scope: rules.BlacklistGroup, GroupJID: string(protected),
		E164: "+966551230077", Hash: hasher.Hash("+966551230077"), Reason: "test", Now: clk.Now()}); err != nil {
		t.Fatal(err)
	}
	card := "BEGIN:VCARD\nVERSION:3.0\nFN:Deals\nTEL;type=CELL;waid=966551230077:+966 55 123 0077\nEND:VCARD"
	deadline := time.Now().Add(10 * time.Second)
	for i := 0; ; i++ {
		e, err := synth.Envelope(synth.Spec{Chat: protected, Sender: "300@lid", ID: wa.MessageID(fmt.Sprintf("V%d", i)), Time: clk.Now(), VCards: []string{card}}, bot)
		if err != nil {
			t.Fatal(err)
		}
		v, err := lane.Process(ctx, e, clk.Now())
		if err != nil {
			t.Fatal(err)
		}
		if v.Source == pipeline.SourceKnown {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the blacklist entry never reached the running lane: last verdict %+v (NOTIFY reload is broken)", v)
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned %v after cancel, want nil", err)
	}
}

// TestLaneMatchesStoredCorpus stores a confirmed message the way a `.spam` confirmation does
// (corpus.FromMessage + store.InsertCorpusEntry) and checks the lane catches a homoglyph variant
// of it as known spam once the corpus index reloads, and ignores provisional entries.
func TestLaneMatchesStoredCorpus(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	clk := clock.NewFake(time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC))
	key := []byte("fastlane-test-number-key-000001")
	hasher, _ := blacklist.NewHasher(key)
	corp := NewCorpus(pool, clk, zerolog.Nop())
	lane, err := New(ctx, Deps{Pool: pool, Clock: clk, Bot: bot, Client: fake.New(), Log: zerolog.Nop(), HashKey: key, Corpus: corp.Indexes})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := lane.Settings.Set(ctx, protected, "200@lid", "mode", json.RawMessage(`"shadow"`)); err != nil {
		t.Fatal(err)
	}
	store1 := func(id wa.MessageID, text string, status corpus.Status) {
		e, err := synth.Envelope(synth.Spec{Chat: protected, Sender: "300@lid", ID: id, Time: clk.Now(), Text: text}, bot)
		if err != nil {
			t.Fatal(err)
		}
		pipeline.NormalizeTexts(e)
		ent := corpus.FromMessage(e, corpus.Options{Scope: corpus.ScopeGroup, GroupJID: string(protected), Status: status,
			Source: corpus.SourceCommand, Hasher: hasher, HashKey: key, Confidence: 1, Now: clk.Now()})
		if _, err := store.InsertCorpusEntry(ctx, pool, ent); err != nil {
			t.Fatal(err)
		}
	}
	store1("C1", spam, corpus.StatusActive)
	provisional := "Limited offer for members only send your bank card number to claim the prize money waiting for you today now"
	store1("C2", provisional, corpus.StatusProvisional)
	if err := corp.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	check := func(id wa.MessageID, text string, wantKnown bool) {
		t.Helper()
		e, err := synth.Envelope(synth.Spec{Chat: protected, Sender: "301@lid", ID: id, Time: clk.Now(), Text: text}, bot)
		if err != nil {
			t.Fatal(err)
		}
		v, err := lane.Process(ctx, e, clk.Now())
		if err != nil {
			t.Fatal(err)
		}
		if got := v.Source == pipeline.SourceKnown; got != wantKnown {
			t.Fatalf("%s: verdict %+v, want known=%v", id, v, wantKnown)
		}
	}
	check("L1", strings.ReplaceAll(spam, "o", "о"), true) // Cyrillic о
	check("L2", provisional, false)                       // provisional entries never act
	check("L3", "see you all at the school gate at five", false)
}

// TestLanesHandleInboxRow feeds an inbox row, encoded exactly as the connector stores it, through
// the inbox's moderation sink: the lane is created for the bot on its first message and records
// the shadow detection; a bot with no connection here is retried, not dropped.
func TestLanesHandleInboxRow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := storetest.Pool(t)
	clk := clock.NewFake(time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC))
	corpusIdx := neardup.NewIndex(neardup.DefaultThresholds())
	corpusIdx.Add("c1", neardup.FingerprintText(spam))
	client := fake.New()
	lanes := NewLanes(Deps{Pool: pool, Clock: clk, Log: zerolog.Nop(), HashKey: []byte("fastlane-test-number-key-000001"),
		Corpus: func(wa.JID) []*neardup.Index { return []*neardup.Index{corpusIdx} }},
		func(b wa.JID) (wa.WAClient, bool) { return client, b == bot })
	done := make(chan error, 1)
	go func() { done <- lanes.Run(ctx) }()
	settings := policy.NewSettingsService(store.NewPolicyStore(pool))
	if _, _, err := settings.Set(ctx, protected, "200@lid", "mode", json.RawMessage(`"shadow"`)); err != nil {
		t.Fatal(err)
	}

	evt, err := synth.Event(synth.Spec{Chat: protected, Sender: "300@lid", ID: "IN1", Time: clk.Now(), Text: spam})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := proto.Marshal(evt.RawMessage)
	info, _ := json.Marshal(evt.Info)
	item := inbox.Item{ID: 1, Bot: bot, Chat: protected, MsgID: "IN1", Kind: inboxenv.KindMessage, Sender: "300@lid", Raw: raw, Info: info}
	mi, err := inbox.DecodeInfo(info)
	if err != nil {
		t.Fatal(err)
	}
	// Lanes start serving once Run has its context; retry briefly like the inbox worker would.
	deadline := time.Now().Add(5 * time.Second)
	for {
		err = lanes.Handle(ctx, item, mi, inbox.Decision{Route: inbox.RouteModerate})
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	rows, err := store.NewPolicyStore(pool).ListDetections(ctx, protected, 0, 10)
	if err != nil || len(rows) != 1 || rows[0].MsgID != "IN1" || rows[0].Mode != "shadow" {
		t.Fatalf("detections %+v, %v; want the shadow detection of IN1", rows, err)
	}

	other := item
	other.Bot = "999@lid"
	if err := lanes.Handle(ctx, other, mi, inbox.Decision{Route: inbox.RouteModerate}); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Handle for an unconnected bot = %v, want ErrNotConnected (retried by the inbox, never dropped)", err)
	}
	corrupt := item
	corrupt.Raw = []byte{0xff, 0xff}
	if err := lanes.Handle(ctx, corrupt, mi, inbox.Decision{Route: inbox.RouteModerate}); !errors.Is(err, inbox.ErrPermanent) {
		t.Fatalf("Handle of a corrupt row = %v, want inbox.ErrPermanent", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestCorpusLearningConfirmAndOverturn: an admin's confirmation makes the next copy known spam at
// once; overturning that message makes it stop matching (DESIGN.md §7.5).
func TestCorpusLearningConfirmAndOverturn(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	clk := clock.NewFake(time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC))
	key := []byte("fastlane-test-number-key-000001")
	hasher, _ := blacklist.NewHasher(key)
	idx := NewCorpus(pool, clk, zerolog.Nop())
	learn := &CorpusLearning{Pool: pool, Clock: clk, Hasher: hasher, Key: key, Index: idx, Log: zerolog.Nop()}
	lane, err := New(ctx, Deps{Pool: pool, Clock: clk, Bot: bot, Client: fake.New(), Log: zerolog.Nop(), HashKey: key, Corpus: idx.Indexes})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := lane.Settings.Set(ctx, protected, "200@lid", "mode", json.RawMessage(`"shadow"`)); err != nil {
		t.Fatal(err)
	}
	known := func(id wa.MessageID, text string) bool {
		t.Helper()
		e, err := synth.Envelope(synth.Spec{Chat: protected, Sender: "301@lid", ID: id, Time: clk.Now(), Text: text}, bot)
		if err != nil {
			t.Fatal(err)
		}
		v, err := lane.Process(ctx, e, clk.Now())
		if err != nil {
			t.Fatal(err)
		}
		return v.Source == pipeline.SourceKnown
	}
	if known("B1", spam) {
		t.Fatal("known before any confirmation")
	}
	id, err := learn.Confirm(ctx, review.Confirmation{Group: protected, Message: wa.MessageRef{Sender: "301@lid", ID: "B1"}, Text: spam, Source: "command", At: clk.Now()})
	if err != nil || id == 0 {
		t.Fatalf("Confirm = %d, %v", id, err)
	}
	if !known("B2", strings.ToUpper(spam)) {
		t.Fatal("a copy of a confirmed message must be known spam at once (v0.1's source of known spam)")
	}
	got, ok, err := learn.EntryFor(ctx, protected, "B1")
	if err != nil || !ok || got != id {
		t.Fatalf("EntryFor = %d, %v, %v; want %d", got, ok, err, id)
	}
	if err := learn.Retract(ctx, id); err != nil {
		t.Fatal(err)
	}
	if known("B3", spam) {
		t.Fatal("an overturned confirmation must stop matching (DESIGN §7.5)")
	}
}

// TestLaneSeesSettingsChangesFromElsewhere: an admin's `.mode paused` saved through another
// settings service (the chat handler, another process) reaches the lane through the settings
// NOTIFY, so the next spam is left alone (DESIGN.md §8.4, §11).
func TestLaneSeesSettingsChangesFromElsewhere(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := storetest.Pool(t)
	clk := clock.NewFake(time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC))
	idx := neardup.NewIndex(neardup.DefaultThresholds())
	idx.Add("c1", neardup.FingerprintText(spam))
	shared := policy.NewSettingsService(store.NewPolicyStore(pool))
	lane, err := New(ctx, Deps{Pool: pool, Clock: clk, Bot: bot, Client: fake.New(), Log: zerolog.Nop(), HashKey: []byte("fastlane-test-number-key-000001"),
		Settings: shared, Corpus: func(wa.JID) []*neardup.Index { return []*neardup.Index{idx} }})
	if err != nil {
		t.Fatal(err)
	}
	// Any saved change makes the group's settings document exist (enforce is the default mode).
	if _, _, err := shared.Set(ctx, protected, "200@lid", "review.timeout", json.RawMessage(`"5h"`)); err != nil {
		t.Fatal(err)
	}
	go func() { _ = store.NewPolicyStore(pool).ListenSettings(ctx, shared.Invalidate) }()
	process := func(id wa.MessageID) pipeline.Verdict {
		t.Helper()
		e, err := synth.Envelope(synth.Spec{Chat: protected, Sender: "301@lid", ID: id, Time: clk.Now(), Text: spam}, bot)
		if err != nil {
			t.Fatal(err)
		}
		v, err := lane.Process(ctx, e, clk.Now())
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if v := process("P1"); len(v.Actions) == 0 {
		t.Fatalf("enforce verdict %+v, want actions", v)
	}
	other := policy.NewSettingsService(store.NewPolicyStore(pool))
	if _, _, err := other.Set(ctx, protected, "200@lid", "mode", json.RawMessage(`"paused"`)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for i := 2; ; i++ {
		v := process(wa.MessageID(fmt.Sprintf("P%d", i)))
		if v.Mode == pipeline.ModePaused && len(v.Actions) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after `.mode paused` elsewhere the lane still decides %+v: settings changes never reach it", v)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestConfirmedSpamWithContactsAndOverturnedVariant goes through the production path: spam with
// a phone number and an email, confirmed by an admin, is known spam when resent verbatim
// (stored and live text are redacted the same way); an overturned near-variant joins the
// allow-list and never matches, even though its entry was the one confirmed (DESIGN.md §7.5).
func TestConfirmedSpamWithContactsAndOverturnedVariant(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	clk := clock.NewFake(time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC))
	key := []byte("fastlane-test-number-key-000001")
	hasher, _ := blacklist.NewHasher(key)
	idx := NewCorpus(pool, clk, zerolog.Nop())
	learn := &CorpusLearning{Pool: pool, Clock: clk, Hasher: hasher, Key: key, Index: idx, Log: zerolog.Nop()}
	lane, err := New(ctx, Deps{Pool: pool, Clock: clk, Bot: bot, Client: fake.New(), Log: zerolog.Nop(), HashKey: key, Corpus: idx.Indexes})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := lane.Settings.Set(ctx, protected, "200@lid", "mode", json.RawMessage(`"shadow"`)); err != nil {
		t.Fatal(err)
	}
	n := 0
	verdict := func(text string) pipeline.Verdict {
		t.Helper()
		n++
		e, err := synth.Envelope(synth.Spec{Chat: protected, Sender: "301@lid", ID: wa.MessageID(fmt.Sprintf("X%d", n)), Time: clk.Now(), Text: text}, bot)
		if err != nil {
			t.Fatal(err)
		}
		v, err := lane.Process(ctx, e, clk.Now())
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, text := range []string{
		"Investment opportunity 300% profit weekly, contact @+966501234567 or +44 7911 123456 for details",
		"Free crypto signals every morning, email me at signals.vip@example.com today to join the group",
	} {
		if _, err := learn.Confirm(ctx, review.Confirmation{Group: protected, Message: wa.MessageRef{Sender: "301@lid", ID: wa.MessageID("C-" + text[:5])}, Text: text, Source: "review", At: clk.Now()}); err != nil {
			t.Fatal(err)
		}
		if v := verdict(text); v.Source != pipeline.SourceKnown {
			t.Fatalf("verbatim resend of confirmed spam %q = %+v, want known (stored and live text must be redacted alike)", text, v)
		}
	}

	variant := "FREE crypto signals every morning!! Email me at signals.vip@example.com today, to join the group!!"
	if v := verdict(variant); v.Source != pipeline.SourceKnown {
		t.Fatalf("near-variant before the overturn = %+v, want known", v)
	}
	if err := learn.AllowFingerprint(ctx, review.FingerprintAllow{Group: protected, Message: wa.MessageRef{ID: "X99"}, Text: variant}); err != nil {
		t.Fatal(err)
	}
	if v := verdict(variant); v.Source == pipeline.SourceKnown || len(v.Actions) != 0 && v.Band == pipeline.BandCertain {
		t.Fatalf("overturned variant = %+v, want no known match: the same false positive must not repeat", v)
	}
}

// TestOverturnAfterDoubleConfirmRetractsEveryEntry: a message two admins confirmed (two entries)
// and then overturned through review.Learning leaves no live entry and no stored text (§7.5).
func TestOverturnAfterDoubleConfirmRetractsEveryEntry(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	clk := clock.NewFake(time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC))
	key := []byte("fastlane-test-number-key-000001")
	hasher, _ := blacklist.NewHasher(key)
	learn := &CorpusLearning{Pool: pool, Clock: clk, Hasher: hasher, Key: key, Index: NewCorpus(pool, clk, zerolog.Nop()), Log: zerolog.Nop()}
	msg := wa.MessageRef{Sender: "301@lid", ID: "C1"}
	for _, src := range []string{"command", "review"} {
		if _, err := learn.Confirm(ctx, review.Confirmation{Group: protected, Message: msg, Text: spam, Source: src, At: clk.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	learning := review.NewLearning(review.LearningDeps{Evidence: review.NewMemoryEvidence(), Corpus: learn, Clock: clk})
	if err := learning.Overturn(ctx, review.Overturn{Group: protected, Message: msg, Actor: "200@lid"}); err != nil {
		t.Fatal(err)
	}
	var live, texts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status IN ('active','promoted')), count(*) FILTER (WHERE text_norm <> '')
		FROM app.corpus_entries WHERE group_jid = $1`, string(protected)).Scan(&live, &texts); err != nil {
		t.Fatal(err)
	}
	if live != 0 || texts != 0 {
		t.Fatalf("after the overturn %d entries are live and %d hold the overturned text; want 0 and 0 (DESIGN §7.5)", live, texts)
	}
	if _, ok, err := learn.EntryFor(ctx, protected, "C1"); err != nil || ok {
		t.Fatalf("EntryFor after the overturn = %v, %v; want none left", ok, err)
	}
}

// panickyMembers panics for one sender, standing in for any bug on a message's path.
type panickyMembers struct{ bad wa.JID }

func (p panickyMembers) Sender(_ context.Context, _ wa.JID, who envelope.Identity) (pipeline.Sender, int64, error) {
	if who.LID == p.bad {
		panic("member lookup exploded")
	}
	return pipeline.Sender{}, 7, nil
}

// TestLanesSurvivePanickingMessage: a message whose processing panics is logged at error level
// with the panic, the stack, the caller and its request_id, counted, and failed permanently;
// the next message is moderated as usual. One bad message never stops the bot.
func TestLanesSurvivePanickingMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := storetest.Pool(t)
	clk := clock.NewFake(time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC))
	idx := neardup.NewIndex(neardup.DefaultThresholds())
	idx.Add("c1", neardup.FingerprintText(spam))
	var logs syncBuffer
	m, err := NewMetrics(nil)
	if err != nil {
		t.Fatal(err)
	}
	lanes := NewLanes(Deps{Pool: pool, Clock: clk, Log: zerolog.New(&logs).Hook(CallerHook{}), HashKey: []byte("fastlane-test-number-key-000001"),
		Metrics: m, Members: panickyMembers{bad: "666@lid"}, Corpus: func(wa.JID) []*neardup.Index { return []*neardup.Index{idx} }},
		func(b wa.JID) (wa.WAClient, bool) { return fake.New(), b == bot })
	done := make(chan error, 1)
	go func() { done <- lanes.Run(ctx) }()
	settings := policy.NewSettingsService(store.NewPolicyStore(pool))
	if _, _, err := settings.Set(ctx, protected, "200@lid", "mode", json.RawMessage(`"shadow"`)); err != nil {
		t.Fatal(err)
	}
	row := func(id wa.MessageID, sender wa.JID) (inbox.Item, wa.MessageInfo) {
		evt, err := synth.Event(synth.Spec{Chat: protected, Sender: sender, ID: id, Time: clk.Now(), Text: spam})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := proto.Marshal(evt.RawMessage)
		info, _ := json.Marshal(evt.Info)
		mi, err := inbox.DecodeInfo(info)
		if err != nil {
			t.Fatal(err)
		}
		return inbox.Item{ID: 1, Bot: bot, Chat: protected, MsgID: id, Kind: inboxenv.KindMessage, Sender: sender, Raw: raw, Info: info}, mi
	}
	handle := func(id wa.MessageID, sender wa.JID) error {
		it, mi := row(id, sender)
		var err error
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if err = lanes.Handle(ctx, it, mi, inbox.Decision{Route: inbox.RouteModerate}); err == nil || errors.Is(err, inbox.ErrPermanent) {
				break
			}
		}
		return err
	}
	if err := handle("BAD", "666@lid"); !errors.Is(err, inbox.ErrPermanent) || !errors.Is(err, ErrPanic) {
		t.Fatalf("panicking message: %v, want a permanent panic error (retrying would panic again)", err)
	}
	out := logs.String()
	for _, want := range []string{`"level":"error"`, `"panic":"member lookup exploded"`, `"stack":"goroutine`, `"request_id":"`, `"caller":"`, `"msg":"BAD"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("log lacks %s:\n%s", want, out)
		}
	}
	if got := testutil.ToFloat64(m.Panics.WithLabelValues("fastlane")); got != 1 {
		t.Fatalf("panics_total{component=fastlane} = %v, want 1", got)
	}
	if err := handle("GOOD", "301@lid"); err != nil {
		t.Fatalf("the next message failed: %v", err)
	}
	rows, err := store.NewPolicyStore(pool).ListDetections(ctx, protected, 0, 10)
	if err != nil || len(rows) != 1 || rows[0].MsgID != "GOOD" {
		t.Fatalf("detections %+v, %v; want the next message moderated", rows, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
