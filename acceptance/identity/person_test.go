package identity_test

// F-020: persons and identities, MatchPerson by LID or PN (DESIGN §7.7, §14).

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

func TestMatchPersonFindsByEitherIdentifier(t *testing.T) {
	h := newHarness(t)
	p := h.learn(t, lidA, pnSA, identity.SourceSenderAlt)
	if p.LID != lidA || p.PN != pnSA {
		t.Fatalf("Learn: got person {LID:%q PN:%q}, want {LID:%q PN:%q} (the person carries both identifiers once linked, DESIGN §7.7)", p.LID, p.PN, lidA, pnSA)
	}
	for _, jid := range []wa.JID{lidA, pnSA} {
		got := h.mustMatch(t, jid)
		if got.ID != p.ID {
			t.Errorf("MatchPerson(%s): got person %d, want %d (bans attach to the person, so either identifier alone must find it, DESIGN §7.7)", jid, got.ID, p.ID)
		}
		if got.LID != lidA || got.PN != pnSA {
			t.Errorf("MatchPerson(%s): got {LID:%q PN:%q}, want {LID:%q PN:%q}", jid, got.LID, got.PN, lidA, pnSA)
		}
	}
}

func TestMatchPersonNormalizesDeviceSuffix(t *testing.T) {
	h := newHarness(t)
	p := h.learn(t, lidA, pnSA, identity.SourceSenderAlt)
	for _, jid := range []wa.JID{
		"100000000000001:5@lid",
		"100000000000001:12@lid",
		"966501234567:3@s.whatsapp.net",
	} {
		got, ok := h.match(t, jid)
		if !ok || got.ID != p.ID {
			t.Errorf("MatchPerson(%s): got (%d, found=%v), want person %d (identities are stored non-AD, so a device suffix must not hide the person, DESIGN §7.7)", jid, got.ID, ok, p.ID)
		}
	}
	again := h.ensure(t, "100000000000001:7@lid", identity.SourceSenderAlt)
	if again.ID != p.ID {
		t.Errorf("Ensure(device form of %s): got person %d, want existing %d (a device suffix must not create a second person, DESIGN §7.7)", lidA, again.ID, p.ID)
	}
	if again.LID != lidA {
		t.Errorf("Ensure(device form): got LID %q, want normalized %q (identities are stored with ToNonAD, DESIGN §7.7)", again.LID, lidA)
	}
}

func TestMatchPersonNeverCreatesOrLooksUp(t *testing.T) {
	h := newHarness(t)
	h.lids.set(lidA, pnSA)
	for _, jid := range []wa.JID{lidA, pnSA} {
		if _, ok := h.match(t, jid); ok {
			t.Fatalf("MatchPerson(%s) on an empty store: found a person, want not found (MatchPerson is a pure lookup)", jid)
		}
	}
	for _, jid := range []wa.JID{lidA, pnSA} {
		if _, ok := h.match(t, jid); ok {
			t.Errorf("MatchPerson(%s) twice: found a person, want not found (MatchPerson must never create a person)", jid)
		}
	}
	if l2p, p2l := h.lids.calls(); l2p != 0 || p2l != 0 {
		t.Errorf("MatchPerson: got LIDForPN=%d PNForLID=%d calls, want 0 and 0 (MatchPerson is a pure lookup in identities, DESIGN §7.7)", l2p, p2l)
	}
}

func TestNonPersonJIDsAreRejected(t *testing.T) {
	h := newHarness(t)
	// A real person whose user part collides with the non-person JIDs below.
	p := h.ensure(t, "120363000000000001@lid", identity.SourceSenderAlt)
	cases := []wa.JID{
		"",
		"120363000000000001@g.us",
		"120363000000000001@newsletter",
		"status@broadcast",
		"120363000000000001",
		"@lid",
		"@s.whatsapp.net",
	}
	ctx := context.Background()
	for _, jid := range cases {
		t.Run(string(jid), func(t *testing.T) {
			if _, err := h.svc.Ensure(ctx, jid, identity.SourceSenderAlt); !errors.Is(err, identity.ErrNotPerson) {
				t.Errorf("Ensure(%q): got err %v, want ErrNotPerson (groups, channels and broadcasts are not persons, DESIGN §7.7)", jid, err)
			}
			if _, err := h.svc.Learn(ctx, jid, pnSA, identity.SourceSenderAlt); !errors.Is(err, identity.ErrNotPerson) {
				t.Errorf("Learn(%q as LID): got err %v, want ErrNotPerson", jid, err)
			}
			if _, err := h.svc.Learn(ctx, lidB, jid, identity.SourceSenderAlt); !errors.Is(err, identity.ErrNotPerson) {
				t.Errorf("Learn(%q as PN): got err %v, want ErrNotPerson", jid, err)
			}
			got, ok, err := h.svc.MatchPerson(ctx, jid)
			if err != nil || ok {
				t.Errorf("MatchPerson(%q): got (person %d, found=%v, err=%v), want not found and no error (a group or channel must never match person %d, or a ban would hit the wrong target)", jid, got.ID, ok, err, p.ID)
			}
		})
	}
	for _, jid := range []wa.JID{lidB, pnSA} {
		if _, ok := h.match(t, jid); ok {
			t.Errorf("MatchPerson(%s): found a person, want not found (a rejected Learn must not create anything)", jid)
		}
	}
}

func TestEnsureIsIdempotent(t *testing.T) {
	h := newHarness(t)
	first := h.ensure(t, lidA, identity.SourceSenderAlt)
	second := h.ensure(t, lidA, identity.SourceParticipant)
	if second.ID != first.ID {
		t.Fatalf("Ensure(%s) twice: got persons %d and %d, want the same (one person per identifier, DESIGN §7.7)", lidA, first.ID, second.ID)
	}

	var wg sync.WaitGroup
	ids := make([]identity.PersonID, 16)
	errs := make([]error, 16)
	for i := range ids {
		wg.Go(func() {
			p, err := h.svc.Ensure(context.Background(), lidC, identity.SourceSenderAlt)
			ids[i], errs[i] = p.ID, err
		})
	}
	wg.Wait()
	for i := range ids {
		if errs[i] != nil {
			t.Fatalf("concurrent Ensure(%s): unexpected error %v", lidC, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("concurrent Ensure(%s): got persons %d and %d, want one (messages from one sender arrive in parallel; a duplicate person splits bans, DESIGN §7.7)", lidC, ids[0], ids[i])
		}
	}
	if ids[0] == first.ID {
		t.Errorf("Ensure(%s): got person %d, the same as %s, want distinct persons", lidC, ids[0], lidA)
	}
}

func TestEnsureLIDOnlyUsesOnlyLocalLIDStore(t *testing.T) {
	t.Run("pn in local store", func(t *testing.T) {
		h := newHarness(t)
		h.lids.set(lidA, pnSA)
		p := h.ensure(t, lidA, identity.SourceSenderAlt)
		if p.LID != lidA || p.PN != pnSA {
			t.Fatalf("Ensure(%s): got {LID:%q PN:%q}, want {LID:%q PN:%q} (the local LID store is a free PN source, DESIGN §7.7)", lidA, p.LID, p.PN, lidA, pnSA)
		}
		row, ok := findIdentity(h.identities(t, p.ID), identity.KindPN, pnSA)
		if !ok || row.Source != identity.SourceLIDStore {
			t.Errorf("Identities: got pn row %+v (found=%v), want source %q (each identity row records where it came from, DESIGN §14)", row, ok, identity.SourceLIDStore)
		}
		if got := h.mustMatch(t, pnSA); got.ID != p.ID {
			t.Errorf("MatchPerson(%s): got person %d, want %d", pnSA, got.ID, p.ID)
		}
		if l2p, _ := h.lids.calls(); l2p != 0 {
			t.Errorf("LIDForPN calls: got %d, want 0 (we already have the LID; no lookups beyond the free sources, DESIGN §7.7)", l2p)
		}
	})
	t.Run("pn unknown", func(t *testing.T) {
		h := newHarness(t)
		p := h.ensure(t, lidA, identity.SourceSenderAlt)
		if p.LID != lidA || p.PN != "" {
			t.Fatalf("Ensure(%s): got {LID:%q PN:%q}, want {LID:%q PN:\"\"} (a missing phone number is normal, DESIGN §7.7)", lidA, p.LID, p.PN, lidA)
		}
		h.ensure(t, lidA, identity.SourceSenderAlt)
		if l2p, _ := h.lids.calls(); l2p != 0 {
			t.Errorf("LIDForPN calls: got %d, want 0 (there is no LID-to-PN lookup beyond the free sources, DESIGN §7.7)", l2p)
		}
		resolved, _ := h.events.snapshot()
		if len(resolved) != 0 {
			t.Errorf("Resolved events: got %d, want 0 (no phone number was learned)", len(resolved))
		}
	})
}

func TestEnsurePNOnlyLooksUpLIDOnce(t *testing.T) {
	h := newHarness(t)
	h.lids.set(lidA, pnSA)
	p := h.ensure(t, pnSA, identity.SourceParticipant)
	if p.LID != lidA || p.PN != pnSA {
		t.Fatalf("Ensure(%s): got {LID:%q PN:%q}, want {LID:%q PN:%q} (a PN-only person gets its LID looked up and the LID becomes primary, DESIGN §7.7)", pnSA, p.LID, p.PN, lidA, pnSA)
	}
	if got := h.mustMatch(t, lidA); got.ID != p.ID {
		t.Errorf("MatchPerson(%s): got person %d, want %d (the looked-up LID must find the same person)", lidA, got.ID, p.ID)
	}
	h.ensure(t, pnSA, identity.SourceParticipant)
	h.ensure(t, lidA, identity.SourceSenderAlt)
	if l2p, _ := h.lids.calls(); l2p != 1 {
		t.Errorf("LIDForPN calls: got %d, want exactly 1 (looked up once on creation, never again once the LID is known; usync is rate-limited, DESIGN §7.7, §10.3)", l2p)
	}
}

func TestEnsurePNOnlyUnresolvedDoesNotRepeatLookup(t *testing.T) {
	h := newHarness(t)
	p := h.ensure(t, pnSA, identity.SourceParticipant)
	if p.PN != pnSA || p.LID != "" {
		t.Fatalf("Ensure(%s) with no LID known: got {LID:%q PN:%q}, want a PN-only person {LID:\"\" PN:%q}", pnSA, p.LID, p.PN, pnSA)
	}
	for range 5 {
		if again := h.ensure(t, pnSA, identity.SourceParticipant); again.ID != p.ID {
			t.Fatalf("Ensure(%s) again: got person %d, want %d (one person per identifier)", pnSA, again.ID, p.ID)
		}
	}
	if l2p, _ := h.lids.calls(); l2p != 1 {
		t.Errorf("LIDForPN calls after 6 Ensure of one PN at the same instant: got %d, want 1 (one lookup per new PN-only person, not one per message; usync is rate-limited, DESIGN §7.7, §10.3)", l2p)
	}
}

func TestEnsurePNLookupFailureStillCreatesPerson(t *testing.T) {
	h := newHarness(t)
	h.lids.lidForPNErr = errors.New("usync: rate limited")
	p := h.ensure(t, pnSA, identity.SourceParticipant)
	if p.PN != pnSA || p.LID != "" {
		t.Fatalf("Ensure(%s) with a failing lookup: got {LID:%q PN:%q}, want PN-only {LID:\"\" PN:%q} (the record works without the LID; a failed lookup must not drop the person, DESIGN §7.7)", pnSA, p.LID, p.PN, pnSA)
	}
	if got := h.mustMatch(t, pnSA); got.ID != p.ID {
		t.Errorf("MatchPerson(%s): got person %d, want %d", pnSA, got.ID, p.ID)
	}
}

func TestEnsurePNResolvesToExistingLIDPerson(t *testing.T) {
	h := newHarness(t)
	lidPerson := h.ensure(t, lidA, identity.SourceSenderAlt)
	h.lids.set(lidA, pnSA)
	p := h.ensure(t, pnSA, identity.SourceParticipant)
	if p.ID != lidPerson.ID {
		t.Fatalf("Ensure(%s) whose LID is %s: got person %d, want the existing LID person %d (the LID is primary; one human must be one person, DESIGN §7.7)", pnSA, lidA, p.ID, lidPerson.ID)
	}
	if got := h.mustMatch(t, pnSA); got.ID != lidPerson.ID {
		t.Errorf("MatchPerson(%s): got person %d, want %d", pnSA, got.ID, lidPerson.ID)
	}
	resolved, _ := h.events.snapshot()
	if len(resolved) != 1 || resolved[0].Person.ID != lidPerson.ID || resolved[0].PN != pnSA {
		t.Errorf("Resolved events: got %+v, want exactly one for person %d with PN %s (learning a LID person's number triggers back-fill, DESIGN §7.7)", resolved, lidPerson.ID, pnSA)
	}
}
