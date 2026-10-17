package fake

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
)

func TestFakeScript(t *testing.T) {
	c := New(ai.Caps{NativeSchema: true})
	boom := errors.New("down")
	c.Push(Response{Verdict: ai.Verdict{Label: ai.LabelSpam, Confidence: 0.95}}, Response{Err: boom})
	ctx := context.Background()
	req := ai.ClassifyRequest{Message: ai.Message{Kind: "text"}}

	if v, err := c.Classify(ctx, req); err != nil || v.Label != ai.LabelSpam {
		t.Fatalf("call 1: got %+v, %v, want the first scripted verdict", v, err)
	}
	if _, err := c.Classify(ctx, req); !errors.Is(err, boom) {
		t.Fatalf("call 2: got %v, want the scripted error", err)
	}
	if v, err := c.Classify(ctx, req); err != nil || v.Label != ai.LabelUnsure {
		t.Fatalf("call 3: got %+v, %v, want unsure by default (the safe answer)", v, err)
	}
	if c.Calls() != 3 || len(c.Requests()) != 3 || !c.Capabilities().NativeSchema {
		t.Fatal("got wrong bookkeeping, want 3 recorded requests and the given caps")
	}
}

func TestFakeBlockUntilTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := New(ai.Caps{})
		c.SetDefault(Response{Block: true})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := c.Classify(ctx, ai.ClassifyRequest{}); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v, want DeadlineExceeded (a hung provider must surface as a timeout)", err)
		}
	})
}
