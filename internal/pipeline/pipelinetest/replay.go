//go:build integration

package pipelinetest

import (
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/replay"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Replay feeds fixture records through the harness (Postgres, the queue and the fake client)
// and returns the report (DESIGN.md §18, F-089). It fails the test on any error.
func (h *Harness) Replay(records []replay.Record) replay.Report {
	h.t.Helper()
	rep, err := replay.Run(target{h}, records)
	if err != nil {
		h.t.Fatalf("pipelinetest: replay: %v", err)
	}
	return rep
}

// target adapts the harness to replay.Target.
type target struct{ h *Harness }

func (t target) AddGroup(r replay.Record) error {
	g := Group{JID: r.Group, Settings: Settings{Mode: r.Mode, Preset: Preset(r.Preset), Enforcement: Enforcement(r.Enforcement), ReviewGroup: r.ReviewGroup}}
	for _, m := range r.Admins {
		g.Admins = append(g.Admins, Member(m))
	}
	for _, m := range r.Members {
		g.Members = append(g.Members, Member(m))
	}
	t.h.AddGroup(g)
	return nil
}

func (t target) Join(group wa.JID, m replay.Member) error { t.h.Join(group, Member(m)); return nil }

func (t target) ConfirmSpam(group wa.JID, text string) error {
	t.h.ConfirmSpam(group, text)
	return nil
}

func (t target) Blacklist(group wa.JID, number string) error {
	t.h.Blacklist(group, number)
	return nil
}

func (t target) Advance(d time.Duration) error { t.h.clk.Advance(d); return nil }

func (t target) Deliver(r replay.Record) (pipeline.Verdict, error) {
	return t.h.Deliver(Message{Group: r.Group, Sender: r.Sender, SenderPN: r.SenderPN, ID: r.ID, Time: r.Time, Text: r.Text, VCards: r.VCards}), nil
}
