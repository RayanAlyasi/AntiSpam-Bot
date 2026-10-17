package actions_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
)

// TestNULIsStrippedOrRefused: text can carry attacker-supplied NULs, which Postgres can't store;
// they're dropped from text and refused in keys and IDs, the same on every store.
func TestNULIsStrippedOrRefused(t *testing.T) {
	ctx := context.Background()
	s := actions.NewMemStore(nil)
	a := send("700@s.whatsapp.net", "s1", "hi\x00 there", actions.PurposeReview, "a\x00b")
	a.Bot = bot
	id := mustEnqueue(ctx, t, s, a)
	got := mustGet(ctx, t, s, id)
	if got.Payload.Text != "hi there" || strings.ContainsRune(strings.Join(got.Payload.Variants, ""), 0) {
		t.Errorf("stored text %q, variants %q; want the NULs dropped", got.Payload.Text, got.Payload.Variants)
	}
	onlyNUL := send("700@s.whatsapp.net", "s2", "\x00", actions.PurposeDM)
	onlyNUL.Bot = bot
	if _, err := s.Enqueue(ctx, onlyNUL); !errors.Is(err, actions.ErrInvalid) {
		t.Errorf("a send whose text is only NUL: err = %v, want ErrInvalid (nothing left to send)", err)
	}
	bad := revoke(group(1), "m\x00", time.Now(), actions.P1)
	bad.Bot = bot
	if _, err := s.Enqueue(ctx, bad); !errors.Is(err, actions.ErrInvalid) {
		t.Errorf("a message ID with a NUL: err = %v, want ErrInvalid", err)
	}
}
