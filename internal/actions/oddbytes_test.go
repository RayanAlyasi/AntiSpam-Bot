package actions_test

import (
	"context"
	"errors"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// oddBytesSuite: every store accepts and refuses the same bytes, so an attacker-supplied quote
// can't make Postgres abort the detection's transaction where MemStore wouldn't.
func oddBytesSuite(t *testing.T, s actions.Store) {
	ctx := context.Background()
	refused := map[string]func(*actions.Action){
		"NUL in ReplyTo":           func(a *actions.Action) { a.Payload.ReplyTo = &wa.MessageRef{ID: "m\x00", Sender: member(1)} },
		"invalid UTF-8 key":        func(a *actions.Action) { a.DedupeKey = "k\xff" },
		"invalid UTF-8 chat":       func(a *actions.Action) { a.Chat = "1203\xff@g.us" },
		"invalid UTF-8 message ID": func(a *actions.Action) { a.Payload.Target.ID = "\xfe" },
	}
	for name, change := range refused {
		a := send(group(1), "odd-"+name, "hi", actions.PurposeNotice)
		a.Kind, a.Payload.Target = actions.KindReact, wa.MessageRef{ID: "x", Sender: member(1)}
		a.Bot = bot
		change(&a)
		if _, err := s.Enqueue(ctx, a); !errors.Is(err, actions.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	a := send("700@s.whatsapp.net", "odd-text", "bad \xff byte", actions.PurposeDM)
	a.Bot = bot
	id := mustEnqueue(ctx, t, s, a)
	if got := mustGet(ctx, t, s, id); got.Payload.Text != "bad � byte" {
		t.Errorf("text with invalid UTF-8 stored as %q, want it replaced with U+FFFD", got.Payload.Text)
	}
}

func TestOddBytesMemStore(t *testing.T) { oddBytesSuite(t, actions.NewMemStore(nil)) }
