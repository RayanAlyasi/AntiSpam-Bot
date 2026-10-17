package policy

import (
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

func enforcePlan(kinds ...ActionKind) Plan {
	p := Plan{Record: true, Mode: DetectionEnforce}
	for _, k := range kinds {
		prio := map[ActionKind]int{ActionDelete: P1, ActionBan: P2, ActionReview: P3}[k]
		p.Enqueue = append(p.Enqueue, Action{Kind: k, Priority: prio})
	}
	p.Would = p.Enqueue
	return p
}

func count(p Plan, k ActionKind) int {
	n := 0
	for _, a := range p.Enqueue {
		if a.Kind == k {
			n++
		}
	}
	return n
}

func TestBanCapDefersToOneReview(t *testing.T) {
	c := clock.NewFake(t0)
	r := NewRails(c)
	s := Defaults()
	s.Limits.MaxBansPerHour = 3
	batches := map[string]bool{}
	alerts := 0
	for i := range 5 {
		p, al := r.Admit("g1", s, enforcePlan(ActionDelete, ActionBan))
		alerts += len(al)
		if count(p, ActionDelete) != 1 {
			t.Fatalf("ban %d: the delete was dropped; the cap only defers bans", i)
		}
		if i < 3 {
			if count(p, ActionBan) != 1 {
				t.Fatalf("ban %d of 3 allowed: got %+v, want the ban", i, p.Enqueue)
			}
			continue
		}
		if count(p, ActionBan) != 0 || count(p, ActionReview) != 1 {
			t.Fatalf("ban %d over the cap: got %+v, want a review instead (never dropped, §8.6)", i, p.Enqueue)
		}
		rv := p.Enqueue[len(p.Enqueue)-1].Review
		if rv.Batch == "" || !rv.ProposeBan || rv.OnTimeout == TimeoutAutoBan {
			t.Fatalf("deferred ban review = %+v, want a batched ban proposal that can't ban on timeout", rv)
		}
		batches[rv.Batch] = true
	}
	if len(batches) != 1 || alerts != 1 {
		t.Errorf("deferred bans went to %d review items with %d alerts, want 1 item and 1 alert", len(batches), alerts)
	}
	// Another group has its own cap.
	if p, _ := r.Admit("g2", s, enforcePlan(ActionBan)); count(p, ActionBan) != 1 {
		t.Error("g2 was capped by g1's bans")
	}
	// An hour later the window has room again.
	c.Advance(time.Hour + time.Second)
	if p, _ := r.Admit("g1", s, enforcePlan(ActionBan)); count(p, ActionBan) != 1 {
		t.Error("after an hour: ban still deferred, want it admitted")
	}
}

func TestBanCapSeededFromStore(t *testing.T) {
	r := NewRails(clock.NewFake(t0))
	s := Defaults()
	s.Limits.MaxBansPerHour = 2
	r.NoteBans("g", t0.Add(-10*time.Minute), t0.Add(-2*time.Hour), t0.Add(-5*time.Minute))
	if p, _ := r.Admit("g", s, enforcePlan(ActionBan)); count(p, ActionBan) != 0 {
		t.Error("two bans in the last hour from before a restart: ban admitted, want it deferred")
	}
}

func TestDeletionCircuit(t *testing.T) {
	c := clock.NewFake(t0)
	r := NewRails(c)
	s := Defaults()
	s.Limits.MaxDeletionsPerMinute = 3
	for i := range 3 {
		if p, al := r.Admit("g", s, enforcePlan(ActionDelete)); count(p, ActionDelete) != 1 || len(al) != 0 {
			t.Fatalf("delete %d within the limit: got %+v, %v", i, p, al)
		}
	}
	p, al := r.Admit("g", s, enforcePlan(ActionDelete, ActionBan))
	if len(p.Enqueue) != 0 || p.Mode != DetectionShadow || !p.Record || len(al) != 1 || al[0].Kind != AlertDeletionCircuit {
		t.Fatalf("4th delete in a minute: got %+v, %v; want nothing enqueued, recorded as shadow, one alert", p, al)
	}
	c.Advance(time.Hour)
	if p, al := r.Admit("g", s, enforcePlan(ActionDelete)); len(p.Enqueue) != 0 || len(al) != 0 {
		t.Fatalf("circuit open: got %+v, %v; want the group paused until reset, no repeat alert", p, al)
	}
	if !r.CircuitOpen("g") || r.CircuitOpen("other") {
		t.Error("CircuitOpen is wrong")
	}
	r.ResetCircuit("g")
	if p, _ := r.Admit("g", s, enforcePlan(ActionDelete)); count(p, ActionDelete) != 1 {
		t.Error("after reset: delete not admitted")
	}
}

func TestKillSwitches(t *testing.T) {
	r := NewRails(clock.NewFake(t0))
	s := Defaults()
	r.SetGlobalKill(true)
	if p, _ := r.Admit("g", s, enforcePlan(ActionDelete, ActionBan)); len(p.Enqueue) != 0 || p.Mode != DetectionShadow {
		t.Fatalf("global kill: got %+v, want nothing enqueued", p)
	}
	r.SetGlobalKill(false)

	f := Finding{
		Band: BandCertain, Source: SourceKnown, Type: TypeText,
		Signals:    []Signal{{Code: "blocklist.crypto", Weight: 4, Known: true}, {Code: "kw.free", Weight: 0.5}},
		Violations: []Violation{{Rule: "content.links"}},
	}
	if got := r.FilterFinding(f); got.Band != BandCertain || len(got.Signals) != 2 {
		t.Fatalf("nothing killed: finding changed to %+v", got)
	}
	r.KillRule("blocklist.crypto")
	r.KillRule("content.links")
	if !r.Killed("blocklist.crypto") {
		t.Fatal("Killed = false after KillRule")
	}
	got := r.FilterFinding(f)
	if got.Band != BandStillUncertain || got.Source != SourceRules || len(got.Signals) != 1 || len(got.Violations) != 0 {
		t.Fatalf("killed the only known evidence: got %+v, want still uncertain from rules, no violation", got)
	}
	if d := Decide(s, got, Standing{Role: RoleMember}); d.Delete || d.Ban {
		t.Fatalf("decision on a killed rule: %+v, want no delete or ban", d)
	}
	r.KillRule("kw.free")
	if got := r.FilterFinding(f); got.Band != BandClean {
		t.Fatalf("every signal killed: band %s, want clean", got.Band)
	}
	r.ReviveRule("blocklist.crypto")
	if r.Killed("blocklist.crypto") {
		t.Error("Killed = true after ReviveRule")
	}
}

func TestAdmitLeavesShadowAlone(t *testing.T) {
	r := NewRails(clock.NewFake(t0))
	s := Defaults()
	s.Limits.MaxDeletionsPerMinute = 1
	p := Plan{Record: true, Mode: DetectionShadow, Would: enforcePlan(ActionDelete, ActionDelete).Enqueue}
	for range 3 {
		if got, al := r.Admit("g", s, p); len(got.Enqueue) != 0 || len(al) != 0 || got.Mode != DetectionShadow {
			t.Fatalf("shadow plan changed: %+v, %v", got, al)
		}
	}
	if r.CircuitOpen("g") {
		t.Error("shadow would-be deletions tripped the circuit")
	}
}

func TestKilledContentRuleIsSilent(t *testing.T) {
	r := NewRails(clock.NewFake(t0))
	r.KillRule("content.videos")
	f := Finding{Band: BandCertain, Type: TypeVideos, Violations: []Violation{{Rule: "content.videos"}}, At: t0}
	got := r.FilterFinding(f)
	if got.Band != BandClean || len(got.Violations) != 0 {
		t.Fatalf("killed content rule: finding %+v, want clean", got)
	}
	if d := Decide(Defaults(), got, Standing{Role: RoleMember}); d.Delete || d.Ban || d.Review != nil {
		t.Fatalf("killed content rule: decision %+v, want nothing (a kill switch silences the rule)", d)
	}
}
