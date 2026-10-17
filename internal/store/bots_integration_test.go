//go:build integration

package store_test

import (
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

func TestBotLease(t *testing.T) {
	pool := storetest.Pool(t)
	ctx := t.Context()
	bots := store.NewBots(pool)
	const bot = wa.JID("966500000000@s.whatsapp.net")

	if ok, err := bots.Acquire(ctx, bot, "a", time.Minute); err != nil || ok {
		t.Fatalf("Acquire on a missing bot = %v, %v; want false (no row, no lease)", ok, err)
	}
	if err := bots.UpsertBot(ctx, bot, "9@lid", "Assistant", "active"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := bots.Acquire(ctx, bot, "a", time.Minute); !ok {
		t.Fatal("a: Acquire on a free lease = false")
	}
	if ok, _ := bots.Acquire(ctx, bot, "b", time.Minute); ok {
		t.Fatal("b took a lease a holds: two processes would connect one session (StreamReplaced)")
	}
	if ok, _ := bots.Acquire(ctx, bot, "a", time.Minute); !ok {
		t.Error("a: re-acquiring its own lease = false")
	}
	if ok, _ := bots.Renew(ctx, bot, "b", time.Minute); ok {
		t.Error("b renewed a lease it doesn't hold")
	}

	// a stops renewing; the lease expires and b takes it.
	if _, err := pool.Exec(ctx, "UPDATE app.bots SET lease_until = now() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := bots.Renew(ctx, bot, "a", time.Minute); ok {
		t.Error("a renewed an expired lease; it must reconnect through Acquire")
	}
	if ok, _ := bots.Acquire(ctx, bot, "b", time.Minute); !ok {
		t.Fatal("b: Acquire on an expired lease = false")
	}
	if ok, _ := bots.Renew(ctx, bot, "a", time.Minute); ok {
		t.Error("a renewed after b took the lease")
	}
	if err := bots.Release(ctx, bot, "a"); err != nil {
		t.Fatal(err)
	}
	if b, _, _ := bots.Bot(ctx, bot); b.LeaseOwner != "b" {
		t.Errorf("a's Release removed b's lease (owner %q)", b.LeaseOwner)
	}
	if err := bots.Release(ctx, bot, "b"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := bots.Acquire(ctx, bot, "a", time.Minute); !ok {
		t.Error("a: Acquire after b released = false")
	}
}

func TestBotUpsertKeepsStatus(t *testing.T) {
	pool := storetest.Pool(t)
	ctx := t.Context()
	bots := store.NewBots(pool)
	const bot = wa.JID("966500000000@s.whatsapp.net")
	_ = bots.UpsertBot(ctx, bot, "", "One", "active")
	if err := bots.SetBotStatus(ctx, bot, "paused"); err != nil {
		t.Fatal(err)
	}
	_ = bots.UpsertBot(ctx, bot, "9@lid", "Two", "active")
	b, ok, err := bots.Bot(ctx, bot)
	if err != nil || !ok || b.Status != "paused" || b.LID != "9@lid" || b.DisplayName != "Two" {
		t.Errorf("bot %+v: want paused kept (an operator pause survives a restart) and LID/name updated", b)
	}
	_ = bots.UpsertBot(ctx, bot, "", "", "active") // re-pairing before the LID is known
	if b, _, _ := bots.Bot(ctx, bot); b.LID != "9@lid" || b.DisplayName != "Two" {
		t.Errorf("bot %+v after an upsert without LID/name: want both kept", b)
	}
	if err := bots.SetBotStatus(ctx, "1@s.whatsapp.net", "active"); err == nil {
		t.Error("SetBotStatus on an unknown bot: nil error")
	}
	if err := bots.SetBotStatus(ctx, bot, "bogus"); err == nil {
		t.Error("SetBotStatus with an unknown status: nil error (the CHECK constraint must hold)")
	}
	if list, _ := bots.List(ctx); len(list) != 1 {
		t.Errorf("List = %d bots, want 1", len(list))
	}
}
