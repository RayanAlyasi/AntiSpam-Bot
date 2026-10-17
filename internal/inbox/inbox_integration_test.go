//go:build integration

package inbox

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const bot = wa.JID("966500000000@s.whatsapp.net")

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := storetest.Pool(t)
	if _, err := pool.Exec(t.Context(), "INSERT INTO app.bots (jid) VALUES ($1)", bot); err != nil {
		t.Fatal(err)
	}
	return pool
}

func msg(id string, edit string) *wa.MessageEvent {
	return &wa.MessageEvent{
		Bot: bot, Raw: []byte{1, 2},
		Info:     wa.MessageInfo{Chat: "1@g.us", Sender: "5@lid", ID: wa.MessageID(id), Edit: edit},
		InfoJSON: []byte(`{"Chat":"1@g.us","Sender":"5@lid","ID":"` + id + `","IsGroup":true}`),
	}
}

func count(t *testing.T, pool *pgxpool.Pool, where string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM app.inbox WHERE "+where).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWriterDedupes(t *testing.T) {
	pool := newPool(t)
	w := NewWriter(pool, clock.NewFake(t0))
	ctx := t.Context()
	undec := &wa.UndecryptableEvent{Bot: bot, Info: wa.MessageInfo{Chat: "1@g.us", ID: "A"}, InfoJSON: []byte(`{}`), UnavailableType: "view_once"}
	for _, e := range []wa.Event{msg("A", ""), msg("A", ""), msg("A", "7"), undec, undec, &wa.ConnectionEvent{Bot: bot}} {
		if err := w.HandleEvent(ctx, e); err != nil {
			t.Fatalf("HandleEvent(%T): %v (a redelivery must not fail, or the message is never acked)", e, err)
		}
	}
	if n := count(t, pool, "true"); n != 3 {
		t.Errorf("inbox rows = %d, want 3 (message, its revoke, the undecryptable copy; redeliveries deduped)", n)
	}
	if err := w.HandleEvent(ctx, &wa.MessageEvent{Bot: bot, Info: wa.MessageInfo{ID: "B"}, InfoJSON: []byte("nope")}); err == nil {
		t.Error("invalid info JSON accepted")
	}
	if err := w.HandleEvent(ctx, &wa.MessageEvent{Bot: "1@s.whatsapp.net", Info: wa.MessageInfo{Chat: "1@g.us", ID: "C"}, InfoJSON: []byte(`{}`)}); err == nil {
		t.Error("message for an unknown bot accepted; want an error so it stays unacked")
	}
}

func TestWriterKeepsAnotherSendersReusedID(t *testing.T) {
	pool := newPool(t)
	w := NewWriter(pool, clock.NewFake(t0))
	ctx := t.Context()
	member := msg("SAME", "")
	spammer := msg("SAME", "")
	spammer.Info.Sender = "6@lid" // message IDs are chosen by the sending client
	for _, e := range []wa.Event{member, spammer, spammer} {
		if err := w.HandleEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, pool, "msg_id = 'SAME'"); n != 2 {
		t.Errorf("rows for one ID from two senders = %d, want 2 (a reused ID must not hide a message from moderation)", n)
	}
}

func TestShutdownHandsRowsBackWithoutSpendingAttempts(t *testing.T) {
	pool := newPool(t)
	ctx, cancel := context.WithCancel(t.Context())
	w := NewWriter(pool, clock.NewFake(t0))
	for _, id := range []string{"a", "b", "c"} {
		_ = w.HandleEvent(ctx, msg(id, ""))
	}
	wk := NewWorker(pool, HandlerFunc(func(ctx context.Context, it Item) error {
		cancel() // SIGTERM arrives while the first row is being handled
		return ctx.Err()
	}), WorkerOptions{Batch: 10, Log: zerolog.Nop()})
	_, _ = wk.ProcessBatch(ctx)
	if n := count(t, pool, "claimed_until IS NULL AND attempts = 0 AND processed_at IS NULL"); n != 3 {
		t.Errorf("rows handed back unspent = %d, want 3 (a shutdown must not use up retries)", n)
	}
}

func TestWorkerProcessesRetriesAndGivesUp(t *testing.T) {
	pool := newPool(t)
	ctx := t.Context()
	w := NewWriter(pool, clock.NewFake(t0))
	for _, id := range []string{"ok", "flaky", "bad", "poison"} {
		_ = w.HandleEvent(ctx, msg(id, ""))
	}
	var flakyCalls atomic.Int32
	h := HandlerFunc(func(_ context.Context, it Item) error {
		switch it.MsgID {
		case "flaky":
			if flakyCalls.Add(1) == 1 {
				return errors.New("db blip")
			}
		case "bad":
			return errors.New("always fails")
		case "poison":
			return ErrPermanent
		}
		return nil
	})
	wk := NewWorker(pool, h, WorkerOptions{Batch: 10, MaxAttempts: 2, Log: zerolog.Nop()})
	if n, err := wk.ProcessBatch(ctx); err != nil || n != 4 {
		t.Fatalf("first batch: %d, %v; want 4 claimed", n, err)
	}
	if n := count(t, pool, "processed_at IS NOT NULL"); n != 2 {
		t.Errorf("processed after one pass = %d, want 2 (ok and the permanent failure)", n)
	}
	if n, _ := wk.ProcessBatch(ctx); n != 0 {
		t.Errorf("retry claimed %d rows before their backoff, want 0", n)
	}
	_, _ = pool.Exec(ctx, "UPDATE app.inbox SET claimed_until = now() - interval '1 second' WHERE processed_at IS NULL")
	if n, _ := wk.ProcessBatch(ctx); n != 2 {
		t.Errorf("second pass claimed %d, want 2", n)
	}
	if n := count(t, pool, "processed_at IS NOT NULL"); n != 4 {
		t.Errorf("processed = %d, want 4 (flaky succeeded, bad gave up after 2 attempts)", n)
	}
	if n := count(t, pool, "msg_id = 'bad' AND last_error = 'always fails'"); n != 1 {
		t.Error("given-up row doesn't record its error")
	}
}

func TestCrashedClaimIsResumed(t *testing.T) {
	pool := newPool(t)
	ctx := t.Context()
	_ = NewWriter(pool, clock.NewFake(t0)).HandleEvent(ctx, msg("A", ""))
	// A worker claims the row and dies before finishing it.
	crashed := NewWorker(pool, nil, WorkerOptions{Log: zerolog.Nop()})
	if rows, err := crashed.q.ClaimInbox(ctx, db.ClaimInboxParams{ClaimSeconds: 300, RowLimit: 10}); err != nil || len(rows) != 1 {
		t.Fatalf("claim: %d rows, %v", len(rows), err)
	}

	var got []wa.MessageID
	other := NewWorker(pool, HandlerFunc(func(_ context.Context, it Item) error { got = append(got, it.MsgID); return nil }), WorkerOptions{Log: zerolog.Nop()})
	if n, _ := other.ProcessBatch(ctx); n != 0 {
		t.Fatal("another worker took a row under a live claim")
	}
	_, _ = pool.Exec(ctx, "UPDATE app.inbox SET claimed_until = now() - interval '1 second'")
	if n, _ := other.ProcessBatch(ctx); n != 1 || len(got) != 1 {
		t.Errorf("after the claim expired: claimed %d, handled %v; want the row resumed", n, got)
	}
}

func TestWorkersShareRowsAndWakeOnNotify(t *testing.T) {
	pool := newPool(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var mu sync.Mutex
	seen := map[wa.MessageID]int{}
	done := make(chan struct{}, 100)
	h := HandlerFunc(func(_ context.Context, it Item) error {
		mu.Lock()
		seen[it.MsgID]++
		mu.Unlock()
		done <- struct{}{}
		return nil
	})
	// Two "processes" with a long poll: only NOTIFY can wake them in time.
	for range 2 {
		wk := NewWorker(pool, h, WorkerOptions{Workers: 3, Batch: 2, Poll: time.Hour, Log: zerolog.Nop()})
		go func() { _ = wk.Run(ctx) }()
	}
	time.Sleep(300 * time.Millisecond) // let the listeners start
	w := NewWriter(pool, clock.Real{})
	const n = 20
	for i := range n {
		_ = w.HandleEvent(ctx, msg(string(rune('a'+i)), ""))
	}
	timeout := time.After(10 * time.Second)
	for range n {
		select {
		case <-done:
		case <-timeout:
			t.Fatalf("handled %d of %d rows within 10 s; the NOTIFY wake-up isn't working", len(seen), n)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for id, c := range seen {
		if c != 1 {
			t.Errorf("row %s handled %d times, want exactly once (SKIP LOCKED claims)", id, c)
		}
	}
}

func TestPurgeAndMessageIndex(t *testing.T) {
	pool := newPool(t)
	ctx := t.Context()
	w := NewWriter(pool, clock.NewFake(t0))
	_ = w.HandleEvent(ctx, msg("old", ""))
	w.clk = clock.NewFake(t0.Add(InboxRetention))
	_ = w.HandleEvent(ctx, msg("new", ""))

	idx := NewIndex(pool)
	sim := uint64(1) << 63 // the top bit must survive the bigint column
	if err := idx.Put(ctx, IndexEntry{Group: "1@g.us", MsgID: "n", At: t0, SimHash: &sim, URLDomains: []string{"x.com"}}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Put(ctx, IndexEntry{Group: "1@g.us", MsgID: "s", At: t0, Verdict: VerdictSpam}); err != nil {
		t.Fatal(err)
	}
	// A later clean write (an edit re-analysed as clean) never lowers a spam verdict.
	_ = idx.Put(ctx, IndexEntry{Group: "1@g.us", MsgID: "s", At: t0, Verdict: VerdictClean})
	got, ok, _ := idx.Get(ctx, "1@g.us", "s")
	if !ok || got.Verdict != VerdictSpam {
		t.Errorf("verdict %q, want spam kept", got.Verdict)
	}
	if n, _, _ := idx.Get(ctx, "1@g.us", "n"); n.SimHash == nil || *n.SimHash != sim || len(n.URLDomains) != 1 {
		t.Errorf("entry %+v: want simhash and domains round-tripped", n)
	}
	// The dispatcher's base row (a retry) must not erase what detection recorded.
	_ = idx.Put(ctx, IndexEntry{Group: "1@g.us", MsgID: "n", At: t0, SenderPerson: 0})
	if n, _, _ := idx.Get(ctx, "1@g.us", "n"); n.SimHash == nil || len(n.URLDomains) != 1 {
		t.Errorf("after a base-row rewrite: %+v, want the fingerprints kept", n)
	}
	_ = idx.Put(ctx, IndexEntry{Group: "1@g.us", MsgID: "s", At: t0, Verdict: VerdictPolicy})
	if s, _, _ := idx.Get(ctx, "1@g.us", "s"); s.Verdict != VerdictSpam {
		t.Errorf("policy after spam: verdict %q, want spam kept (clean < policy < spam)", s.Verdict)
	}
	if err := idx.Put(ctx, IndexEntry{Group: "1@lid", MsgID: "x", At: t0}); err == nil {
		t.Error("index entry for a non-group accepted")
	}

	in, ix, err := Purge(ctx, pool, t0.Add(InboxRetention+time.Minute))
	if err != nil || in != 1 || ix != 0 {
		t.Errorf("purge at 72 h: %d inbox, %d index rows, %v; want only the old inbox row", in, ix, err)
	}
	_, ix, _ = Purge(ctx, pool, t0.Add(IndexRetention))
	if ix != 1 {
		t.Errorf("purge at 7 d removed %d index rows, want 1 (the clean one)", ix)
	}
	_, ix, _ = Purge(ctx, pool, t0.Add(IndexActionRetention))
	if ix != 1 {
		t.Errorf("purge at 30 d removed %d index rows, want 1 (the spam one)", ix)
	}
}
