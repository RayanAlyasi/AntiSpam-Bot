package aicontext

import (
	"strings"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

func TestRemovedMessageNeverReachesTheAI(t *testing.T) {
	b := NewBuffer(clock.NewFake(t0), 0, 0)
	fill(b, msg("p1", time.Minute, "deleted for everyone"), msg("p2", 30*time.Second, "still here"))
	b.Remove(chat, "p1")
	c, _, _ := Build(t.Context(), b, input(msg("x", 0, "judged")))
	if len(c.Previous) != 1 || c.Previous[0].Text != "still here" {
		t.Fatalf("previous = %+v, want the revoked message gone", c.Previous)
	}
}

func TestCapDropsOldestPreviousFirst(t *testing.T) {
	b := NewBuffer(clock.NewFake(t0), 0, 0)
	orig := msg("orig", 9*time.Minute, strings.Repeat("o", 200))
	b.Add(chat, orig)
	for i := range 5 {
		r := msg(string(rune('a'+i)), -time.Duration(i+1)*time.Second, strings.Repeat("r", 200))
		r.ReplyTo = "x"
		b.Add(chat, r)
	}
	for i := range 5 {
		b.Add(chat, msg(string(rune('k'+i)), time.Duration(8-i)*time.Minute, strings.Repeat("p", 200)))
	}
	judged := msg("x", 0, "judged")
	judged.ReplyTo = "orig"
	in := input(judged)
	in.Group.Name = strings.Repeat("n", 200)
	in.Group.Topic = strings.Repeat("t", 200)
	in.Group.Allowed = strings.Repeat("a", 200)
	c, _, _ := Build(t.Context(), b, in)
	if Size(c) > DefaultTotalRunes {
		t.Fatalf("size %d over the cap", Size(c))
	}
	// 600 (group) + 200 (replied-to) + 1,000 (replies) leaves room for one previous message: the
	// four oldest go; the replied-to message and the replies stay.
	if len(c.Previous) != 1 || c.Previous[0].Text != strings.Repeat("p", 200) || c.ReplyTo == nil || len(c.Replies) != 5 {
		t.Fatalf("previous=%d replyTo=%v replies=%d; want the oldest previous messages dropped first",
			len(c.Previous), c.ReplyTo != nil, len(c.Replies))
	}
}
