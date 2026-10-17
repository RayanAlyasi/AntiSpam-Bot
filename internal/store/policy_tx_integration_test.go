//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

func shadowSpamRecord(t *testing.T, msg string, edit int) policy.DetectionRecord {
	t.Helper()
	s := policy.Defaults()
	f := policy.Finding{
		Band: policy.BandCertain, Source: policy.SourceKnown, Score: 0.97, Type: policy.TypeText,
		Signals: []policy.Signal{{Code: "url.blocklisted", Weight: 5, Known: true}},
	}
	d := policy.Decide(s, f, policy.Standing{Role: policy.RoleMember})
	rec, ok := policy.NewDetection(policy.DetectionRef{Group: pgroup, MsgID: wa.MessageID("MSG-" + msg), EditSeq: edit, TierReached: "t0", RuleVersion: "w1"}, f, d, policy.PlanFor(s, d))
	if !ok {
		t.Fatal("spam not recorded")
	}
	return rec
}

func TestDetectionDedupesRedeliveredEvents(t *testing.T) {
	ctx := context.Background()
	ps := store.NewPolicyStore(storetest.Pool(t))
	rec := shadowSpamRecord(t, "A", 0)
	first, created, err := ps.RecordDetection(ctx, rec)
	if err != nil || !created {
		t.Fatalf("first record = %v, %v", created, err)
	}
	again, created, err := ps.RecordDetection(ctx, rec)
	if err != nil || created || again.ID != first.ID || !again.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("redelivered event = id %d created %v, %v; want the existing id %d and created=false", again.ID, created, err, first.ID)
	}
	later := rec
	later.Score, later.Route, later.ActionPlan = 0.5, policy.RouteNone, nil
	stored, created, err := ps.RecordDetection(ctx, later)
	if err != nil || created || stored.Score != rec.Score || stored.Route != rec.Route || len(stored.ActionPlan) != len(rec.ActionPlan) ||
		stored.ActionPlan[len(stored.ActionPlan)-1].Kind != rec.ActionPlan[len(rec.ActionPlan)-1].Kind ||
		len(stored.Signals) != 1 || stored.Reason != rec.Reason || stored.EditSeq != 0 {
		t.Fatalf("later decision on the same version = %+v, %v, %v; want the stored first record back unchanged", stored, created, err)
	}
	edited := shadowSpamRecord(t, "A", 1)
	if r, created, err := ps.RecordDetection(ctx, edited); err != nil || !created || r.ID == first.ID {
		t.Errorf("the message's first edit = id %d created %v, %v; want a new detection", r.ID, created, err)
	}
	rescan := rec
	rescan.Mode = policy.DetectionNetworkRescan
	if _, created, err := ps.RecordDetection(ctx, rescan); err != nil || !created {
		t.Errorf("a network re-scan of the same message = created %v, %v; want its own detection", created, err)
	}
	rows, _ := ps.ListDetections(ctx, pgroup, 0, 10)
	if len(rows) != 3 {
		t.Errorf("stored %d detections, want 3 (original, edit, re-scan)", len(rows))
	}
	bad := rec
	bad.EditSeq = -1
	if _, _, err := ps.RecordDetection(ctx, bad); err == nil {
		t.Error("negative edit stored, want refused")
	}
}

func TestPolicyStoreInCallerTransaction(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	ps := store.NewPolicyStore(pool)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	doc, _ := json.Marshal(policy.Defaults())

	// The caller's transaction fails after the writes: nothing may remain.
	boom := errors.New("enqueue failed")
	err := store.InTx(ctx, pool, func(tx pgx.Tx) error {
		txs := ps.WithTx(tx)
		if _, err := txs.InsertDetection(ctx, shadowSpamRecord(t, "B", 0)); err != nil {
			return err
		}
		if _, _, err := txs.CreateBan(ctx, policy.GroupBan{Group: pgroup, PersonID: 9, Reason: policy.BanSpam, DecidedBy: policy.DecidedAuto, RejoinBlock: true, CreatedAt: now}); err != nil {
			return err
		}
		if _, err := txs.SaveSettings(ctx, policy.SettingsChange{Group: pgroup, Doc: doc, Actor: "a"}); err != nil {
			return err
		}
		if err := txs.ListenSettings(ctx, nil); err == nil {
			t.Error("ListenSettings inside a transaction = nil, want an error")
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InTx = %v, want the caller's error", err)
	}
	if rows, _ := ps.ListDetections(ctx, pgroup, 0, 10); len(rows) != 0 {
		t.Errorf("rolled back: %d detections remain, want none", len(rows))
	}
	if _, ok, _ := ps.ActiveBan(ctx, pgroup, 9); ok {
		t.Error("rolled back: the ban remains")
	}
	if _, ok, _ := ps.LoadSettings(ctx, pgroup); ok {
		t.Error("rolled back: the settings remain")
	}

	// The same writes commit together.
	err = store.InTx(ctx, pool, func(tx pgx.Tx) error {
		txs := ps.WithTx(tx)
		if _, err := txs.InsertDetection(ctx, shadowSpamRecord(t, "B", 0)); err != nil {
			return err
		}
		_, _, err := txs.CreateBan(ctx, policy.GroupBan{Group: pgroup, PersonID: 9, Reason: policy.BanSpam, DecidedBy: policy.DecidedAuto, RejoinBlock: true, CreatedAt: now})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows, _ := ps.ListDetections(ctx, pgroup, 0, 10); len(rows) != 1 {
		t.Errorf("committed: %d detections, want 1", len(rows))
	}
	if _, ok, _ := ps.ActiveBan(ctx, pgroup, 9); !ok {
		t.Error("committed: no active ban")
	}
}
