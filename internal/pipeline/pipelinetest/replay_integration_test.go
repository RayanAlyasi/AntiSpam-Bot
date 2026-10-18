//go:build integration

package pipelinetest

import (
	"os"
	"slices"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/replay"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// TestReplayFixtureEndToEnd replays the v0.1 fixture through Postgres, the queue and the fake
// client, checks it agrees with the in-memory simulator, and that WhatsApp saw exactly the
// enforced group's deletes and kick and nothing for the shadow group.
func TestReplayFixtureEndToEnd(t *testing.T) {
	f, err := os.Open("../../replay/testdata/v01.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := replay.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	h := New(t, Options{})
	got := h.Replay(recs)

	sim, err := replay.NewSim(replay.SimOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want, err := replay.Run(sim, recs)
	if err != nil {
		t.Fatal(err)
	}
	if d := replay.Diff(want, got); len(d) != 0 {
		t.Fatalf("the Postgres harness and the simulator disagree: %+v", d)
	}
	revokes := h.WA().CallsTo(fake.MethodRevoke)
	if len(revokes) != 4 {
		t.Fatalf("WhatsApp saw %d revokes, want 4 (S1-S3 and S5 in the enforced group; shadow S4 acts on nothing)", len(revokes))
	}
	var kicked []wa.JID
	for _, c := range h.WA().CallsTo(fake.MethodUpdateParticipants) {
		if c.Args[0] != wa.JID("120363000000000001@g.us") {
			t.Fatalf("kick in %v, want only the enforced group", c.Args[0])
		}
		kicked = append(kicked, c.Args[1].([]wa.JID)...)
	}
	slices.Sort(kicked)
	if !slices.Equal(kicked, []wa.JID{"300000000000001@lid", "300000000000003@lid"}) {
		t.Fatalf("kicked %v, want the two new members who posted known spam (S1, S5)", kicked)
	}
	for _, c := range h.WA().CallsTo(fake.MethodSend) {
		if slices.Contains([]any{wa.JID("120363000000000001@g.us"), wa.JID("120363000000000002@g.us")}, c.Args[0]) {
			t.Fatalf("sent to a protected group %v: the bot is silent (DESIGN §9.7)", c.Args[0])
		}
	}
}
