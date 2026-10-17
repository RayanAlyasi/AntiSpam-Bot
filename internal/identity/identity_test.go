package identity

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

type recEvents struct {
	resolved []ResolvedEvent
	recycled []RecycledEvent
	fail     error
}

func (r *recEvents) Resolved(_ context.Context, ev ResolvedEvent) error {
	r.resolved = append(r.resolved, ev)
	return r.fail
}

func (r *recEvents) RecycledNumber(_ context.Context, ev RecycledEvent) error {
	r.recycled = append(r.recycled, ev)
	return r.fail
}

type mapLIDs struct {
	pnToLID map[wa.JID]wa.JID
	lidToPN map[wa.JID]wa.JID
	err     error
}

func (m *mapLIDs) LIDForPN(_ context.Context, pn wa.JID) (wa.JID, error) { return m.pnToLID[pn], m.err }

func (m *mapLIDs) PNForLID(_ context.Context, lid wa.JID) (wa.JID, error) {
	return m.lidToPN[lid], m.err
}

func newService(lids LIDStore) (*Service, *recEvents, *clock.Fake) {
	ev := &recEvents{}
	clk := clock.NewFake(t0)
	return New(NewMemoryStore(), lids, ev, clk), ev, clk
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		in       wa.JID
		want     wa.JID
		wantKind Kind
		ok       bool
	}{
		{"123@lid", "123@lid", KindLID, true},
		{"123:5@lid", "123@lid", KindLID, true},
		{"9665.1:3@s.whatsapp.net", "9665@s.whatsapp.net", KindPN, true},
		{" 9665@s.whatsapp.net ", "9665@s.whatsapp.net", KindPN, true},
		{"1203@g.us", "", "", false},
		{"status@broadcast", "", "", false},
		{"123@newsletter", "", "", false},
		{"abc@lid", "", "", false},
		{"@lid", "", "", false},
		{"123", "", "", false},
		{"", "", "", false},
	}
	for _, tt := range tests {
		got, kind, ok := normalize(tt.in)
		if got != tt.want || kind != tt.wantKind || ok != tt.ok {
			t.Errorf("normalize(%q) = %q %q %v, want %q %q %v", tt.in, got, kind, ok, tt.want, tt.wantKind, tt.ok)
		}
	}
}

func FuzzNormalize(f *testing.F) {
	for _, s := range []string{"123@lid", "1:2@s.whatsapp.net", "x@g.us", "", "@", "1.2.3:4@lid"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got, kind, ok := normalize(wa.JID(s))
		if !ok {
			return
		}
		again, kind2, ok2 := normalize(got)
		if !ok2 || again != got || kind2 != kind {
			t.Errorf("normalize not idempotent: %q → %q → %q", s, got, again)
		}
		if kind != KindLID && kind != KindPN {
			t.Errorf("normalize(%q) kind %q", s, kind)
		}
	})
}

func TestCountryCodeAndNumberPrefix(t *testing.T) {
	tests := []struct {
		pn         wa.JID
		cc, prefix string
	}{
		{"966501234567@s.whatsapp.net", "966", "96650"},
		{"447700900123@s.whatsapp.net", "44", "4477"},
		{"14155550123:2@s.whatsapp.net", "1", "141"},
		{"201001234567@s.whatsapp.net", "20", "2010"},
		{"123@lid", "", ""},
		{"0@s.whatsapp.net", "", ""},
	}
	for _, tt := range tests {
		if got := CountryCode(tt.pn); got != tt.cc {
			t.Errorf("CountryCode(%s) = %q, want %q", tt.pn, got, tt.cc)
		}
		if got := NumberPrefix(tt.pn); got != tt.prefix {
			t.Errorf("NumberPrefix(%s) = %q, want %q", tt.pn, got, tt.prefix)
		}
	}
}

func TestEventErrorIsReportedAfterTheWriteIsKept(t *testing.T) {
	svc, ev, _ := newService(nil)
	ev.fail = errors.New("queue down")
	if _, err := svc.Learn(t.Context(), "1@lid", "9665@s.whatsapp.net", SourceSenderAlt); err == nil {
		t.Fatal("Learn with failing Events: nil error, want the event error reported")
	}
	if p, ok, _ := svc.MatchPerson(t.Context(), "9665@s.whatsapp.net"); !ok || p.LID != "1@lid" {
		t.Errorf("after event failure: MatchPerson = %+v %v, want the stored pair kept", p, ok)
	}
}

func TestLearnPNMovesBetweenLIDsOfOnePersonClosesOldNumber(t *testing.T) {
	svc, ev, _ := newService(nil)
	ctx := t.Context()
	if _, err := svc.Learn(ctx, "1@lid", "9661@s.whatsapp.net", SourceSenderAlt); err != nil {
		t.Fatal(err)
	}
	p, err := svc.Learn(ctx, "1@lid", "9662@s.whatsapp.net", SourceSenderAlt)
	if err != nil {
		t.Fatal(err)
	}
	if p.PN != "9662@s.whatsapp.net" {
		t.Errorf("PN = %s, want the newest number", p.PN)
	}
	if _, ok, _ := svc.MatchPerson(ctx, "9661@s.whatsapp.net"); ok {
		t.Error("old number still matches the person, want its link closed (one current number per person)")
	}
	if len(ev.resolved) != 2 || len(ev.recycled) != 0 {
		t.Errorf("events: %d resolved, %d recycled; want 2, 0", len(ev.resolved), len(ev.recycled))
	}
}

func TestEnsureIgnoresSameKindLookupResults(t *testing.T) {
	// A misbehaving LIDStore returning a PN for a PN must not create a bogus link.
	svc, _, _ := newService(&mapLIDs{pnToLID: map[wa.JID]wa.JID{"9665@s.whatsapp.net": "9666@s.whatsapp.net"}})
	p, err := svc.Ensure(t.Context(), "9665@s.whatsapp.net", SourceEvent)
	if err != nil {
		t.Fatal(err)
	}
	if p.LID != "" || p.PN != "9665@s.whatsapp.net" {
		t.Errorf("Ensure = %+v, want a PN-only person", p)
	}
}

func TestMergeCombinesMembershipsOfSharedGroups(t *testing.T) {
	svc, _, clk := newService(nil)
	ctx := t.Context()
	g := wa.JID("1@g.us")
	pnOnly, _ := svc.Ensure(ctx, "9665@s.whatsapp.net", SourceEvent)
	if err := svc.MemberJoined(ctx, g, pnOnly.ID, t0); err != nil {
		t.Fatal(err)
	}
	_ = svc.CountMessage(ctx, g, pnOnly.ID, true)
	clk.Advance(time.Hour)
	lidP, _ := svc.Ensure(ctx, "1@lid", SourceEvent)
	_ = svc.SeedMember(ctx, g, lidP.ID, RoleAdmin, false)
	_ = svc.CountMessage(ctx, g, lidP.ID, false)

	if _, err := svc.Learn(ctx, "1@lid", "9665@s.whatsapp.net", SourceSenderAlt); err != nil {
		t.Fatal(err)
	}
	m, ok, _ := svc.Member(ctx, g, lidP.ID)
	if !ok {
		t.Fatal("membership lost in merge")
	}
	if m.MsgCount != 2 || m.CleanCount != 1 || m.Role != RoleAdmin || !m.FirstSeen.Equal(t0) || !m.JoinedAt.Equal(t0) {
		t.Errorf("merged member %+v: want counts 2/1, role admin and the earliest first-seen/joined time", m)
	}
	if _, ok, _ := svc.Member(ctx, g, pnOnly.ID); ok {
		t.Error("merged-away person still has a membership")
	}
}

// TestIdentityInvariants checks, over random sequences of Ensure and Learn, the invariants the
// rest of the bot relies on (DESIGN.md §7.7): every open identity belongs to exactly one live
// person, a person has at most one open LID and one open PN, the person's fields match its open
// rows, a learned pair always matches one person, and a person with a LID never loses it.
func TestIdentityInvariants(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		svc, _, clk := newService(&mapLIDs{
			pnToLID: map[wa.JID]wa.JID{"9660@s.whatsapp.net": "0@lid"},
			lidToPN: map[wa.JID]wa.JID{"1@lid": "9661@s.whatsapp.net"},
		})
		ctx := context.Background()
		lid := func() wa.JID { return wa.JID(fmt.Sprintf("%d@lid", rapid.IntRange(0, 4).Draw(rt, "lid"))) }
		pn := func() wa.JID { return wa.JID(fmt.Sprintf("966%d@s.whatsapp.net", rapid.IntRange(0, 4).Draw(rt, "pn"))) }
		hadLID := map[PersonID]wa.JID{}

		for range rapid.IntRange(1, 25).Draw(rt, "steps") {
			clk.Advance(time.Minute)
			var p Person
			var err error
			switch rapid.IntRange(0, 2).Draw(rt, "op") {
			case 0:
				p, err = svc.Ensure(ctx, lid(), SourceEvent)
			case 1:
				p, err = svc.Ensure(ctx, pn(), SourceEvent)
			case 2:
				l, n := lid(), pn()
				p, err = svc.Learn(ctx, l, n, SourceSenderAlt)
				if err == nil {
					a, _, _ := svc.MatchPerson(ctx, l)
					b, _, _ := svc.MatchPerson(ctx, n)
					if a.ID != b.ID || a.ID != p.ID {
						rt.Fatalf("after Learn(%s,%s): LID → %d, PN → %d, returned %d; want one person", l, n, a.ID, b.ID, p.ID)
					}
				}
			}
			if err != nil {
				rt.Fatalf("op failed: %v", err)
			}
			_ = p
			checkStore(rt, svc, hadLID)
		}
	})
}

func checkStore(rt *rapid.T, svc *Service, hadLID map[PersonID]wa.JID) {
	st := svc.store.(*memStore).read()
	open := map[string]PersonID{}
	perPerson := map[PersonID]map[Kind]wa.JID{}
	for _, r := range st.identities {
		if _, live := st.persons[r.person]; !live {
			rt.Fatalf("identity %s belongs to retired person %d", r.ident.Value, r.person)
		}
		if r.ident.ValidTo != nil {
			continue
		}
		key := string(r.ident.Kind) + "/" + string(r.ident.Value)
		if other, dup := open[key]; dup {
			rt.Fatalf("open identity %s linked to persons %d and %d", key, other, r.person)
		}
		open[key] = r.person
		if perPerson[r.person] == nil {
			perPerson[r.person] = map[Kind]wa.JID{}
		}
		if prev, dup := perPerson[r.person][r.ident.Kind]; dup {
			rt.Fatalf("person %d has two open %s identities: %s and %s", r.person, r.ident.Kind, prev, r.ident.Value)
		}
		perPerson[r.person][r.ident.Kind] = r.ident.Value
	}
	for id, p := range st.persons {
		if p.LID != perPerson[id][KindLID] || p.PN != perPerson[id][KindPN] {
			rt.Fatalf("person %+v disagrees with its open rows %v", p, perPerson[id])
		}
		if p.LID == "" && p.PN == "" {
			rt.Fatalf("person %d has no identifier", id)
		}
		if old, ok := hadLID[id]; ok && p.LID != old {
			rt.Fatalf("person %d's LID changed from %s to %q; the LID is the primary identity", id, old, p.LID)
		}
		if p.LID != "" {
			hadLID[id] = p.LID
		}
	}
}
