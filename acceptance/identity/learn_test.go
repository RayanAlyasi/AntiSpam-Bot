package identity_test

// F-021: learn phone numbers from free sources (DESIGN §7.7, §14).

import (
	"context"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

func TestLearnRecordsSourceOnIdentityRow(t *testing.T) {
	sources := []identity.Source{
		identity.SourceSenderAlt,
		identity.SourceParticipant,
		identity.SourceEvent,
		identity.SourceLIDStore,
		identity.SourceDM,
		identity.SourceSelfVCard,
		identity.SourceUsync,
	}
	want := map[identity.Source]string{
		identity.SourceSenderAlt: "sender_alt", identity.SourceParticipant: "participant",
		identity.SourceEvent: "event", identity.SourceLIDStore: "lid_store", identity.SourceDM: "dm",
		identity.SourceSelfVCard: "self_vcard", identity.SourceUsync: "usync",
	}
	for _, src := range sources {
		t.Run(string(src), func(t *testing.T) {
			if string(src) != want[src] {
				t.Fatalf("source constant: got %q, want %q (values are stored in identities.source, DESIGN §14)", src, want[src])
			}
			h := newHarness(t)
			p := h.learn(t, lidA, pnSA, src)
			ids := h.identities(t, p.ID)
			row, ok := findIdentity(ids, identity.KindPN, pnSA)
			if !ok {
				t.Fatalf("Identities(%d): got %+v, want a pn row for %s (the learned number is stored as an identity, DESIGN §7.7)", p.ID, ids, pnSA)
			}
			if row.Source != src {
				t.Errorf("pn row source: got %q, want %q (each identity row records where it was learned, DESIGN §14)", row.Source, src)
			}
			if row.ValidTo != nil {
				t.Errorf("pn row ValidTo: got %v, want nil (a fresh link is open)", row.ValidTo)
			}
			if _, ok := findIdentity(ids, identity.KindLID, lidA); !ok {
				t.Errorf("Identities(%d): got %+v, want a lid row for %s (the LID is the primary identity, DESIGN §7.7)", p.ID, ids, lidA)
			}
		})
	}
}

func TestLearnDerivesCountryCode(t *testing.T) {
	cases := []struct {
		pn   wa.JID
		want string
	}{
		{pnSA, "966"},
		{pnUK, "44"},
		{pnUS, "1"},
		{pnEG, "20"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			h := newHarness(t)
			p := h.learn(t, lidA, c.pn, identity.SourceSenderAlt)
			ids := h.identities(t, p.ID)
			row, ok := findIdentity(ids, identity.KindPN, c.pn)
			if !ok || row.CountryCode != c.want {
				t.Errorf("pn row for %s: got %+v (found=%v), want country code %q (derived with phonenumbers for number-prefix stats, DESIGN §7.7)", c.pn, row, ok, c.want)
			}
			if lid, ok := findIdentity(ids, identity.KindLID, lidA); !ok || lid.CountryCode != "" {
				t.Errorf("lid row: got %+v (found=%v), want no country code (a LID reveals no country)", lid, ok)
			}
		})
	}
}

func TestLearnRejectsSwappedPairOrUnknownSource(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.Learn(ctx, pnSA, lidA, identity.SourceSenderAlt); err == nil {
		t.Errorf("Learn(pn, lid): got nil error, want an error (the first argument must be a LID and the second a phone number)")
	}
	if _, err := h.svc.Learn(ctx, lidA, lidB, identity.SourceSenderAlt); err == nil {
		t.Errorf("Learn(lid, lid): got nil error, want an error (a LID is not a phone number)")
	}
	if _, err := h.svc.Learn(ctx, lidA, pnSA, identity.Source("guess")); err == nil {
		t.Errorf("Learn with source %q: got nil error, want an error (only the listed free sources are used, DESIGN §7.7, §14)", "guess")
	}
	for _, jid := range []wa.JID{lidA, lidB, pnSA} {
		if _, ok := h.match(t, jid); ok {
			t.Errorf("MatchPerson(%s): found a person, want not found (a rejected Learn must not create anything)", jid)
		}
	}
}

func TestLearnNewPairEmitsResolvedOnce(t *testing.T) {
	h := newHarness(t)
	p := h.learn(t, lidA, pnSA, identity.SourceDM)
	for range 2 {
		again := h.learn(t, lidA, pnSA, identity.SourceSenderAlt)
		if again.ID != p.ID {
			t.Fatalf("Learn of the same pair again: got person %d, want %d (learning is idempotent)", again.ID, p.ID)
		}
	}
	resolved, recycled := h.events.snapshot()
	if len(resolved) != 1 {
		t.Fatalf("Resolved events: got %d (%+v), want exactly 1 (back-fill runs once per newly linked number; repeats would re-apply bans and stats, DESIGN §7.7)", len(resolved), resolved)
	}
	ev := resolved[0]
	if ev.Person.ID != p.ID || ev.Person.LID != lidA || ev.PN != pnSA || ev.MergedFrom != 0 || ev.Source != identity.SourceDM {
		t.Errorf("Resolved event: got %+v, want person %d with LID %s, PN %s, source %q and no merge", ev, p.ID, lidA, pnSA, identity.SourceDM)
	}
	if len(recycled) != 0 {
		t.Errorf("RecycledNumber events: got %d, want 0 (the same pair is not a recycled number)", len(recycled))
	}
	if n := openCount(h.identities(t, p.ID)); n != 2 {
		t.Errorf("open identities: got %d, want 2 (one lid and one pn row; repeats must not duplicate rows)", n)
	}
}

func TestLearnAddsPNToLIDPerson(t *testing.T) {
	h := newHarness(t)
	lidPerson := h.ensure(t, lidA, identity.SourceSenderAlt)
	p := h.learn(t, lidA, pnSA, identity.SourceSenderAlt)
	if p.ID != lidPerson.ID || p.PN != pnSA {
		t.Fatalf("Learn(%s, %s): got person %d PN %q, want person %d PN %s (the number is added to the existing LID person, DESIGN §7.7)", lidA, pnSA, p.ID, p.PN, lidPerson.ID, pnSA)
	}
	if got := h.mustMatch(t, pnSA); got.ID != lidPerson.ID {
		t.Errorf("MatchPerson(%s): got person %d, want %d", pnSA, got.ID, lidPerson.ID)
	}
	resolved, _ := h.events.snapshot()
	if len(resolved) != 1 || resolved[0].Person.ID != lidPerson.ID || resolved[0].PN != pnSA || resolved[0].MergedFrom != 0 {
		t.Errorf("Resolved events: got %+v, want one for person %d, PN %s, no merge (a learned number triggers back-fill, DESIGN §7.7)", resolved, lidPerson.ID, pnSA)
	}
}

func TestLearnNeverCallsLIDForPN(t *testing.T) {
	h := newHarness(t)
	h.ensure(t, lidA, identity.SourceSenderAlt)
	h.ensure(t, lidB, identity.SourceSenderAlt)
	h.learn(t, lidA, pnSA, identity.SourceSenderAlt)
	h.learn(t, lidC, pnUK, identity.SourceEvent)
	h.learn(t, lidB, pnSA, identity.SourceParticipant) // recycled number
	if l2p, _ := h.lids.calls(); l2p != 0 {
		t.Errorf("LIDForPN calls: got %d, want 0 (Learn already has both identifiers; no lookups beyond the free sources, DESIGN §7.7)", l2p)
	}
}
