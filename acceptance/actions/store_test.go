package actions_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

func TestEnqueueAppliesDefaults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		s := actions.NewMemStore(clock.Real{})
		now := time.Now()
		warn := send(dm(1), "warn", "please stop")
		warn.Payload.Purpose = actions.PurposeWarning
		edit := post(actions.KindEdit, dm(2), actions.P3, "e1")
		cases := []struct {
			name         string
			a            actions.Action
			wantDeadline time.Time
		}{
			{"revoke: message time plus 48 h", revoke(group(1), "m1", actions.P1, 3*time.Hour), now.Add(-3 * time.Hour).Add(actions.RevokeWindow)},
			{"edit: 15 min", edit, now.Add(actions.DefaultEditWindow)},
			{"optional warning: 10 min", warn, now.Add(actions.DefaultWarningWindow)},
			{"lockdown: 1 h", lockdown(group(2), actions.KindAnnounce, "a"), now.Add(actions.DefaultLockdownTTL)},
			{"kick: 24 h", kick(group(3), "ban1", member(1)), now.Add(actions.DefaultTTL)},
		}
		for _, tc := range cases {
			res, err := s.Enqueue(ctx, tc.a)
			if err != nil || !res.Inserted || res.Dropped != "" {
				t.Fatalf("%s: Enqueue = %+v, %v; want inserted with no error (DESIGN §10.1)", tc.name, res, err)
			}
			got, err := s.Get(ctx, res.ID)
			if err != nil {
				t.Fatalf("%s: Get(%d): %v", tc.name, res.ID, err)
			}
			if got.Status != actions.StatusPending {
				t.Errorf("%s: status %s, want pending (a new action waits for the executor, DESIGN §10.1)", tc.name, got.Status)
			}
			if !got.NotBefore.Equal(now) {
				t.Errorf("%s: NotBefore %v, want now %v (zero NotBefore means at once)", tc.name, got.NotBefore, now)
			}
			if !got.Deadline.Equal(tc.wantDeadline) {
				t.Errorf("%s: Deadline %v, want %v (every action has a deadline, DESIGN §10.4)", tc.name, got.Deadline, tc.wantDeadline)
			}
		}
	})
}

func TestEnqueueRejectsInvalid(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		s := actions.NewMemStore(clock.Real{})
		noKey := revoke(group(1), "m1", actions.P1, time.Minute)
		noKey.DedupeKey = ""
		badKind := revoke(group(1), "m2", actions.P1, time.Minute)
		badKind.Kind = "explode"
		badPrio := revoke(group(1), "m3", actions.P1, time.Minute)
		badPrio.Priority = 7
		noJIDs := kick(group(1), "ban", member(1))
		noJIDs.Payload.JIDs = nil
		kickDM := kick(dm(1), "ban", member(1))
		noTargetID := revoke(group(1), "m4", actions.P1, time.Minute)
		noTargetID.Payload.Target.ID = ""
		noSender := revoke(group(1), "m5", actions.P1, time.Minute)
		noSender.Payload.Target.Sender = ""
		cases := map[string]actions.Action{
			"no dedupe key":       noKey,
			"unknown kind":        badKind,
			"priority beyond P4":  badPrio,
			"kick without JIDs":   noJIDs,
			"kick in a DM":        kickDM,
			"revoke without ID":   noTargetID,
			"revoke without from": noSender,
		}
		for name, a := range cases {
			res, err := s.Enqueue(ctx, a)
			if !errors.Is(err, actions.ErrInvalid) {
				t.Errorf("%s: Enqueue error %v, want one wrapping ErrInvalid (an invalid action must never reach WhatsApp, DESIGN §10.1)", name, err)
			}
			if res.Inserted {
				t.Errorf("%s: Inserted = true, want false for an invalid action", name)
			}
		}
		if n := len(s.All()); n != 0 {
			t.Fatalf("store holds %d actions after only invalid enqueues, want 0", n)
		}
	})
}

func TestEnqueueDropsPastDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		s := actions.NewMemStore(clock.Real{})
		late := send(dm(1), "late", "hello")
		late.Deadline = time.Now().Add(-time.Second)
		tooOld := revoke(group(1), "old", actions.P1, 49*time.Hour) // outside WhatsApp's 48 h delete window
		for name, a := range map[string]actions.Action{"explicit past deadline": late, "revoke older than 48 h": tooOld} {
			res, err := s.Enqueue(ctx, a)
			if err != nil {
				t.Errorf("%s: Enqueue error %v, want nil (work that can no longer matter is dropped, not an error, DESIGN §10.1)", name, err)
			}
			if res.Inserted || res.Dropped == "" {
				t.Errorf("%s: Enqueue = %+v, want Inserted false and a Dropped reason (DESIGN §10.4 expiry)", name, res)
			}
		}
		if n := len(s.All()); n != 0 {
			t.Fatalf("store holds %d actions, want 0: an action past its deadline must not be stored (DESIGN §10.4)", n)
		}
	})
}

func TestDedupeAcrossAllStatuses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		s := actions.NewMemStore(clock.Real{})
		for _, final := range []actions.Status{actions.StatusPending, actions.StatusDone, actions.StatusFailed, actions.StatusCancelled} {
			a := revoke(group(1), "m-"+string(final), actions.P1, time.Minute)
			first, err := s.Enqueue(ctx, a)
			if err != nil || !first.Inserted {
				t.Fatalf("first Enqueue = %+v, %v; want inserted", first, err)
			}
			switch final {
			case actions.StatusCancelled:
				if ok, err := s.Cancel(ctx, a.DedupeKey, "sender deleted it"); !ok || err != nil {
					t.Fatalf("Cancel = %v, %v; want true", ok, err)
				}
			case actions.StatusDone, actions.StatusFailed:
				if _, ok, err := s.Claim(ctx, first.ID); !ok || err != nil {
					t.Fatalf("Claim = %v, %v; want true", ok, err)
				}
				if err := s.Finish(ctx, first.ID, final, "", actions.Result{}); err != nil {
					t.Fatalf("Finish: %v", err)
				}
			}
			again, err := s.Enqueue(ctx, a)
			if err != nil || again.Inserted || again.ID != first.ID {
				t.Errorf("re-enqueue after %s = %+v, %v; want Inserted false with ID %d (the dedupe key is unique forever, so retries and edits never delete twice, DESIGN §10.4)",
					final, again, err, first.ID)
			}
		}

		// Through the executor: the same delete enqueued twice reaches WhatsApp once.
		h := newHarness(t, actions.DefaultConfig())
		h.start()
		a := revoke(group(2), "dup", actions.P1, time.Minute)
		h.add(a)
		h.enqueue(a)
		h.advance(10 * time.Second)
		h.enqueue(a)
		h.advance(10 * time.Second)
		if got := revokedIDs(h.rec.to(fake.MethodRevoke)); !slices.Equal(got, []wa.MessageID{"dup"}) {
			t.Fatalf("revoked %v, want exactly one revoke of dup (dedupe prevents double deletes, DESIGN §10.4)", got)
		}
	})
}

func TestCancelPendingRevoke(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		h := newHarness(t, actions.DefaultConfig())
		var p0 []int64
		for i := range 20 { // a P0 backlog keeps the P1 delete waiting
			p0 = append(p0, h.add(revoke(group(100+i), "p0", actions.P0, time.Minute)))
		}
		target := revoke(group(1), "self-deleted", actions.P1, time.Minute)
		id := h.add(target)
		h.start()
		h.advance(3 * time.Second)

		ok, err := h.store.Cancel(ctx, actions.RevokeKey(group(1), "self-deleted"), "sender revoked it")
		if err != nil || !ok {
			t.Fatalf("Cancel(pending revoke) = %v, %v; want true (the sender's REVOKE drops the pending delete, DESIGN §10.4)", ok, err)
		}
		h.exec.Wake()
		h.waitFinal(5*time.Minute, "the P0 backlog drains under the ceiling")

		if got := h.get(id).Status; got != actions.StatusCancelled {
			t.Errorf("cancelled revoke has status %s, want cancelled (DESIGN §10.4)", got)
		}
		if slices.Contains(revokedIDs(h.rec.to(fake.MethodRevoke)), wa.MessageID("self-deleted")) {
			t.Errorf("a cancelled revoke reached WhatsApp, want it never executed (DESIGN §10.4 cancellation)")
		}
		if ok, _ := h.store.Cancel(ctx, actions.RevokeKey(group(100), "p0"), "late"); ok {
			t.Errorf("Cancel of a done action = true, want false (only pending actions can be cancelled)")
		}
		if got := h.get(p0[0]).Status; got != actions.StatusDone {
			t.Errorf("done action has status %s after a late Cancel, want done", got)
		}
		if ok, _ := h.store.Cancel(ctx, "revoke:unknown", "x"); ok {
			t.Errorf("Cancel of an unknown key = true, want false")
		}
	})
}
