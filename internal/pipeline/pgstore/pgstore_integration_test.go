//go:build integration

package pgstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const group wa.JID = "1@g.us"

func detection(msg wa.MessageID) policy.DetectionRecord {
	pid := int64(7)
	return policy.DetectionRecord{Group: group, MsgID: msg, PersonID: &pid, Score: 0.99, TierReached: "t1",
		Signals: []policy.Signal{{Code: "corpus.near_duplicate", Weight: 4, Known: true}}, Verdict: policy.VerdictSpam,
		Source: policy.SourceKnown, Route: policy.RouteAutoBan, RuleVersion: "test", Mode: policy.DetectionEnforce,
		ActionPlan: []policy.Action{{Kind: policy.ActionDelete, Priority: policy.P1}, {Kind: policy.ActionBan, Priority: policy.P2}}}
}

func TestStoreWritesOneTransaction(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	var enqueued []actions.Action
	failNext := false
	s := New(pool, func(ctx context.Context, tx db.DBTX, a actions.Action) error {
		if failNext {
			return errors.New("queue down")
		}
		enqueued = append(enqueued, a)
		return nil
	}, nil)
	write := func(msg wa.MessageID) (bool, error) {
		first := false
		err := s.InTx(ctx, func(ctx context.Context, tx pipeline.Tx) error {
			var err error
			if first, err = tx.Claim(ctx, group, msg, "v1"); err != nil || !first {
				return err
			}
			id, err := tx.InsertDetection(ctx, detection(msg))
			if err != nil {
				return err
			}
			b, created, err := tx.CreateBan(ctx, policy.GroupBan{Group: group, PersonID: 7, Reason: policy.BanSpam,
				DecidedBy: policy.DecidedAuto, DetectionID: &id, RejoinBlock: true, CreatedAt: time.Now()})
			if err != nil {
				return err
			}
			if msg == "M1" && !created || !b.Active {
				t.Errorf("ban %+v created=%v, want a new active ban", b, created)
			}
			return tx.Enqueue(ctx, actions.Action{Chat: group, Kind: actions.KindRevoke, DedupeKey: actions.RevokeKey(group, msg)})
		})
		return first, err
	}

	if first, err := write("M1"); err != nil || !first {
		t.Fatalf("first write: first=%v err=%v", first, err)
	}
	if first, err := write("M1"); err != nil || first {
		t.Fatalf("redelivery: first=%v err=%v; want not first (a detection exists)", first, err)
	}
	rows, err := store.NewPolicyStore(pool).ListDetections(ctx, group, 0, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("detections %d, %v; want exactly one", len(rows), err)
	}
	if string(rows[0].ActionPlan) == "[]" || rows[0].Route != "auto_ban" {
		t.Fatalf("stored row %+v lacks its plan or route", rows[0])
	}

	// A failing enqueue rolls the detection back, so a retry starts clean.
	failNext = true
	if _, err := write("M2"); err == nil {
		t.Fatal("write with a failing queue must fail")
	}
	failNext = false
	if rows, _ := store.NewPolicyStore(pool).ListDetections(ctx, group, 0, 10); len(rows) != 1 {
		t.Fatalf("after a rolled-back write there are %d detections, want 1", len(rows))
	}
	if first, err := write("M2"); err != nil || !first {
		t.Fatalf("retry after rollback: first=%v err=%v; want a fresh claim", first, err)
	}
	// The second ban for the same person returns the active one.
	if b, ok, err := store.NewPolicyStore(pool).ActiveBan(ctx, group, 7); err != nil || !ok || !b.RejoinBlock {
		t.Fatalf("active ban %+v %v %v", b, ok, err)
	}
	if len(enqueued) != 2 {
		t.Fatalf("enqueued %d actions, want 2 (one per committed message)", len(enqueued))
	}
}
