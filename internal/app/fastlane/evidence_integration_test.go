//go:build integration

package fastlane

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/neardup"
	reviewpg "github.com/RayanAlyasi/AntiSpam-Bot/internal/review/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter/synth"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// TestDeletionKeepsEvidence covers F-114 end to end: when the fast lane deletes known spam in an
// enforcing group, the message's redacted text is kept as an evidence snapshot for 30 days, in
// the detection's transaction. Shadow mode deletes nothing, so it keeps nothing.
func TestDeletionKeepsEvidence(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	clk := clock.NewFake(time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC))
	corpus := neardup.NewIndex(neardup.DefaultThresholds())
	corpus.Add("c1", neardup.FingerprintText(spam))
	lane, err := New(ctx, Deps{
		Pool: pool, Clock: clk, Bot: bot, Client: fake.New(), Log: zerolog.Nop(), HashKey: []byte("fastlane-test-number-key-000001"),
		Corpus: func(wa.JID) []*neardup.Index { return []*neardup.Index{corpus} },
	})
	if err != nil {
		t.Fatal(err)
	}
	// A stored settings document makes a group protected: enforce through a changed key (the
	// default mode is already enforce), shadow through the mode.
	for group, kv := range map[wa.JID][2]string{protected: {"review.timeout", `"5h"`}, other: {"mode", `"shadow"`}} {
		if _, _, err := lane.Settings.Set(ctx, group, "200@lid", kv[0], json.RawMessage(kv[1])); err != nil {
			t.Fatal(err)
		}
		e, err := synth.Envelope(synth.Spec{Chat: group, Sender: "300@lid", ID: "EV1", Time: clk.Now(), Text: spam}, bot)
		if err != nil {
			t.Fatal(err)
		}
		v, err := lane.Process(ctx, e, clk.Now())
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: band %s route %s actions %v", group, v.Band, v.Route, v.Actions)
	}

	ev := reviewpg.Evidence{DB: pool}
	s, err := ev.Get(ctx, protected, "EV1")
	if err != nil {
		t.Fatalf("no evidence for the deleted message: %v (every deletion keeps a snapshot, DESIGN §14)", err)
	}
	if !strings.Contains(s.Text, "crypto investment") {
		t.Errorf("evidence text %q: want the deleted message's text kept", s.Text)
	}
	if s.DetectionID == 0 || !s.ExpiresAt.Equal(clk.Now().Add(30*24*time.Hour)) {
		t.Errorf("evidence detection %d expires %v, want it linked to the detection and kept 30 days", s.DetectionID, s.ExpiresAt)
	}
	if _, err := ev.Get(ctx, other, "EV1"); err == nil {
		t.Error("a shadow-mode group kept evidence, want none (nothing was deleted)")
	}
}
