package aicontext

import (
	"fmt"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

func TestBufferKeepsRecentMessagesOnly(t *testing.T) {
	clk := clock.NewFake(t0)
	b := NewBuffer(clk, 3, 30*time.Minute)
	for i := range 5 {
		b.Add(chat, msg(fmt.Sprint(i), time.Duration(5-i)*time.Minute, fmt.Sprint("m", i)))
	}
	if b.Len(chat) != 3 {
		t.Fatalf("buffered %d, want the newest 3", b.Len(chat))
	}
	if _, ok, _ := b.Get(t.Context(), chat, "0"); ok {
		t.Error("the oldest message survived the per-chat bound")
	}
	clk.Advance(time.Hour)
	b.Sweep()
	if b.Len(chat) != 0 {
		t.Errorf("buffered %d after an hour, want everything older than maxAge gone (§17)", b.Len(chat))
	}
}

func TestBufferOrdersAndReplacesEdits(t *testing.T) {
	b := NewBuffer(clock.NewFake(t0), 0, 0)
	b.Add(chat, msg("b", time.Minute, "second"))
	b.Add(chat, msg("a", 2*time.Minute, "first"))
	edited := msg("b", time.Minute, "second, edited")
	b.Add(chat, edited)
	got, _ := b.Before(t.Context(), chat, t0.Add(-time.Hour), t0, 0)
	if len(got) != 2 || got[0].ID != "a" || got[1].Text != "second, edited" {
		t.Fatalf("Before() = %+v, want time order and the edit in place", got)
	}
	b.Forget(chat)
	if b.Len(chat) != 0 {
		t.Fatal("Forget kept messages")
	}
}
