package identity_test

// F-022: merge, back-fill and recycled numbers (DESIGN §7.7, §14).

import (
	"slices"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

func TestAddMemberIsIdempotent(t *testing.T) {
	h := newHarness(t)
	p := h.ensure(t, lidA, identity.SourceSenderAlt)
	h.addMember(t, groupOne, p.ID)
	h.addMember(t, groupOne, p.ID)
	h.addMember(t, groupTwo, p.ID)
	if got, want := h.groupsOf(t, p.ID), sorted(groupOne, groupTwo); !slices.Equal(got, want) {
		t.Errorf("GroupsOf(%d): got %v, want %v (one membership per group and person, DESIGN §14 members PK)", p.ID, got, want)
	}
}

func TestLearnGivesPNOnlyPersonItsLID(t *testing.T) {
	h := newHarness(t)
	pnOnly := h.ensure(t, pnSA, identity.SourceParticipant)
	h.addMember(t, groupOne, pnOnly.ID)
	p := h.learn(t, lidA, pnSA, identity.SourceSenderAlt)
	if p.ID != pnOnly.ID || p.LID != lidA || p.PN != pnSA {
		t.Fatalf("Learn(%s, %s) with only a PN-only person: got person %d {LID:%q PN:%q}, want person %d with both (no separate LID person exists, so the record keeps its ID and the LID becomes primary, DESIGN §7.7)", lidA, pnSA, p.ID, p.LID, p.PN, pnOnly.ID)
	}
	if got := h.mustMatch(t, lidA); got.ID != pnOnly.ID {
		t.Errorf("MatchPerson(%s): got person %d, want %d", lidA, got.ID, pnOnly.ID)
	}
	if got := h.groupsOf(t, pnOnly.ID); !slices.Equal(got, sorted(groupOne)) {
		t.Errorf("GroupsOf(%d): got %v, want [%s] (memberships stay with the person)", pnOnly.ID, got, groupOne)
	}
	resolved, _ := h.events.snapshot()
	if len(resolved) != 1 || resolved[0].Person.ID != pnOnly.ID || resolved[0].Person.LID != lidA || resolved[0].MergedFrom != 0 {
		t.Errorf("Resolved events: got %+v, want one for person %d with LID %s and no merge (DESIGN §7.7)", resolved, pnOnly.ID, lidA)
	}
}

func TestLearnMergesPNOnlyPersonIntoLIDPerson(t *testing.T) {
	h := newHarness(t)
	pnOnly := h.ensure(t, pnSA, identity.SourceParticipant) // PN-addressed group, LID not known yet
	lidPerson := h.ensure(t, lidA, identity.SourceSenderAlt)
	if pnOnly.ID == lidPerson.ID {
		t.Fatalf("setup: PN-only and LID-only persons share ID %d, want two records before the pair is known", pnOnly.ID)
	}
	h.addMember(t, groupOne, pnOnly.ID)
	h.addMember(t, groupThree, pnOnly.ID)
	h.addMember(t, groupTwo, lidPerson.ID)
	h.addMember(t, groupThree, lidPerson.ID) // shared group: must not duplicate or fail

	p := h.learn(t, lidA, pnSA, identity.SourceSenderAlt)
	if p.ID != lidPerson.ID || p.LID != lidA || p.PN != pnSA {
		t.Fatalf("Learn(%s, %s): got person %d {LID:%q PN:%q}, want LID person %d with both (the PN-only record merges into the LID person, DESIGN §7.7)", lidA, pnSA, p.ID, p.LID, p.PN, lidPerson.ID)
	}
	for _, jid := range []wa.JID{lidA, pnSA} {
		if got := h.mustMatch(t, jid); got.ID != lidPerson.ID {
			t.Errorf("MatchPerson(%s) after merge: got person %d, want LID person %d (the PN-only record is gone; a ban by number must hit the merged person, DESIGN §7.7)", jid, got.ID, lidPerson.ID)
		}
	}
	if got, want := h.groupsOf(t, lidPerson.ID), sorted(groupOne, groupTwo, groupThree); !slices.Equal(got, want) {
		t.Errorf("GroupsOf(LID person) after merge: got %v, want %v (memberships are re-pointed to the LID person, once per group, DESIGN §7.7)", got, want)
	}
	if got := h.groupsOf(t, pnOnly.ID); len(got) != 0 {
		t.Errorf("GroupsOf(merged-away person %d): got %v, want none (memberships moved to person %d)", pnOnly.ID, got, lidPerson.ID)
	}
	ids := h.identities(t, lidPerson.ID)
	if _, ok := findIdentity(ids, identity.KindLID, lidA); !ok {
		t.Errorf("Identities(LID person): got %+v, want an open lid row for %s", ids, lidA)
	}
	if row, ok := findIdentity(ids, identity.KindPN, pnSA); !ok || row.ValidTo != nil {
		t.Errorf("Identities(LID person): got pn row %+v (found=%v), want an open pn row for %s (the number moved with the merge)", row, ok, pnSA)
	}

	resolved, recycled := h.events.snapshot()
	if len(resolved) != 1 {
		t.Fatalf("Resolved events: got %d (%+v), want exactly 1 (the merge is the back-fill trigger, DESIGN §7.7)", len(resolved), resolved)
	}
	if ev := resolved[0]; ev.Person.ID != lidPerson.ID || ev.PN != pnSA || ev.MergedFrom != pnOnly.ID {
		t.Errorf("Resolved event: got %+v, want person %d, PN %s, MergedFrom %d (back-fill applies bans recorded against the merged-away record, DESIGN §7.7)", ev, lidPerson.ID, pnSA, pnOnly.ID)
	}
	if len(recycled) != 0 {
		t.Errorf("RecycledNumber events: got %+v, want none (a PN-only record has no LID, so this is not a recycled number)", recycled)
	}

	h.learn(t, lidA, pnSA, identity.SourceParticipant)
	if again := h.ensure(t, pnSA, identity.SourceParticipant); again.ID != lidPerson.ID {
		t.Errorf("Ensure(%s) after merge: got person %d, want %d (the merged-away record must not come back)", pnSA, again.ID, lidPerson.ID)
	}
	if resolved, _ := h.events.snapshot(); len(resolved) != 1 {
		t.Errorf("Resolved events after repeating the pair: got %d, want still 1 (merge and back-fill are idempotent, DESIGN §7.7)", len(resolved))
	}
	if l2p, _ := h.lids.calls(); l2p != 1 {
		t.Errorf("LIDForPN calls: got %d, want 1 (only when the PN-only person was created)", l2p)
	}
}

func TestRecycledNumberClosesOldLinkAndNeverMerges(t *testing.T) {
	for _, newLIDKnown := range []bool{false, true} {
		name := "new lid unseen"
		if newLIDKnown {
			name = "new lid already a person"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			oldOwner := h.learn(t, lidA, pnSA, identity.SourceSenderAlt)
			h.addMember(t, groupOne, oldOwner.ID)
			var existing identity.Person
			if newLIDKnown {
				existing = h.ensure(t, lidB, identity.SourceSenderAlt)
				h.addMember(t, groupTwo, existing.ID)
			}
			h.clk.Advance(90 * 24 * time.Hour)
			recycledAt := h.clk.Now()
			before, _ := h.events.snapshot()

			newOwner := h.learn(t, lidB, pnSA, identity.SourceParticipant)
			if newOwner.ID == oldOwner.ID {
				t.Fatalf("Learn(%s, %s) for a number known under %s: got the old person %d, want a different person (bans never follow a number to a new LID, DESIGN §7.7)", lidB, pnSA, lidA, oldOwner.ID)
			}
			if newLIDKnown && newOwner.ID != existing.ID {
				t.Errorf("new owner: got person %d, want the existing LID person %d", newOwner.ID, existing.ID)
			}
			if newOwner.LID != lidB || newOwner.PN != pnSA {
				t.Errorf("new owner: got {LID:%q PN:%q}, want {LID:%q PN:%q}", newOwner.LID, newOwner.PN, lidB, pnSA)
			}
			if got := h.mustMatch(t, pnSA); got.ID != newOwner.ID {
				t.Errorf("MatchPerson(%s): got person %d, want new owner %d (the number now belongs to the new LID)", pnSA, got.ID, newOwner.ID)
			}
			old := h.mustMatch(t, lidA)
			if old.ID != oldOwner.ID {
				t.Errorf("MatchPerson(%s): got person %d, want old owner %d (the old LID still identifies the old person and their bans)", lidA, old.ID, oldOwner.ID)
			}
			if old.PN == pnSA {
				t.Errorf("old owner PN: got %q, want it no longer current (the old link is closed)", old.PN)
			}

			oldRow, ok := findIdentity(h.identities(t, oldOwner.ID), identity.KindPN, pnSA)
			if !ok {
				t.Fatalf("Identities(old owner): pn row for %s missing, want it kept with valid_to set (closed, not overwritten or deleted, DESIGN §7.7)", pnSA)
			}
			if oldRow.ValidTo == nil || !oldRow.ValidTo.Equal(recycledAt) {
				t.Errorf("old pn row ValidTo: got %v, want %v (closed when the number was seen with a new LID, DESIGN §7.7)", oldRow.ValidTo, recycledAt)
			}
			if newRow, ok := findIdentity(h.identities(t, newOwner.ID), identity.KindPN, pnSA); !ok || newRow.ValidTo != nil {
				t.Errorf("Identities(new owner): got pn row %+v (found=%v), want an open row for %s", newRow, ok, pnSA)
			}

			if got := h.groupsOf(t, oldOwner.ID); !slices.Equal(got, sorted(groupOne)) {
				t.Errorf("GroupsOf(old owner): got %v, want [%s] (a recycled number is not a merge; nothing moves)", got, groupOne)
			}
			if got := h.groupsOf(t, newOwner.ID); slices.Contains(got, groupOne) {
				t.Errorf("GroupsOf(new owner): got %v, must not contain %s (the old owner's memberships must not follow the number)", got, groupOne)
			}

			after, recycled := h.events.snapshot()
			if len(after) != len(before) {
				t.Errorf("Resolved events on a recycled number: got %+v, want none (back-fill would apply bans and watch rules against the number to its new owner, DESIGN §7.7)", after[len(before):])
			}
			if len(recycled) != 1 {
				t.Fatalf("RecycledNumber events: got %d (%+v), want exactly 1 (recycled numbers are flagged for the operator, DESIGN §7.7)", len(recycled), recycled)
			}
			want := identity.RecycledEvent{PN: pnSA, OldLID: lidA, OldPerson: oldOwner.ID, NewLID: lidB, NewPerson: newOwner.ID}
			if recycled[0] != want {
				t.Errorf("RecycledNumber event: got %+v, want %+v", recycled[0], want)
			}
		})
	}
}

func TestRecycledNumberIsIdempotent(t *testing.T) {
	h := newHarness(t)
	h.learn(t, lidA, pnSA, identity.SourceSenderAlt)
	first := h.learn(t, lidB, pnSA, identity.SourceSenderAlt)
	h.clk.Advance(time.Hour)
	again := h.learn(t, lidB, pnSA, identity.SourceSenderAlt)
	if again.ID != first.ID {
		t.Errorf("Learn of the new pair again: got person %d, want %d", again.ID, first.ID)
	}
	if _, recycled := h.events.snapshot(); len(recycled) != 1 {
		t.Errorf("RecycledNumber events: got %d, want 1 (the operator is flagged once per recycled number, not once per message)", len(recycled))
	}
	if n := openCount(h.identities(t, first.ID)); n != 2 {
		t.Errorf("open identities of the new owner: got %d, want 2 (one lid, one pn; repeats must not duplicate rows)", n)
	}
}
