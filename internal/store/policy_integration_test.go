//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const pgroup wa.JID = "120363000000000001@g.us"

func TestSettingsSaveVersionsHistoryAuditNotify(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	ps := store.NewPolicyStore(pool)

	var mu sync.Mutex
	var notified []wa.JID
	lctx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- ps.ListenSettings(lctx, func(g wa.JID) { mu.Lock(); notified = append(notified, g); mu.Unlock() })
	}()
	t.Cleanup(func() {
		stop()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("ListenSettings returned %v, want context.Canceled", err)
		}
	})
	count := func(g wa.JID) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, x := range notified {
			if x == g {
				n++
			}
		}
		return n
	}
	waitListening(t, ctx, pool, func(g wa.JID) bool { return count(g) > 0 })

	svc := policy.NewSettingsService(ps)
	if _, v, err := svc.Get(ctx, pgroup); err != nil || v != 0 {
		t.Fatalf("Get of a new group = v%d, %v; want defaults at version 0", v, err)
	}
	if _, _, err := svc.Set(ctx, pgroup, "966500000001", "limits.max_bans_per_hour", json.RawMessage(`4`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ApplyPreset(ctx, pgroup, "966500000002", policy.PresetStrict); err != nil {
		t.Fatal(err)
	}
	stored, ok, err := ps.LoadSettings(ctx, pgroup)
	if err != nil || !ok || stored.Version != 2 || stored.UpdatedBy != "966500000002" {
		t.Fatalf("LoadSettings = v%d by %q, %v, %v; want v2 by the second actor", stored.Version, stored.UpdatedBy, ok, err)
	}
	s, err := policy.ParseStored(stored.Doc)
	if err != nil || s.Preset != policy.PresetStrict || s.Limits.MaxBansPerHour != 4 {
		t.Fatalf("stored document = %s cap %d, %v; want strict with the cap kept", s.Preset, s.Limits.MaxBansPerHour, err)
	}
	h, err := ps.SettingsHistory(ctx, pgroup, 10)
	if err != nil || len(h) != 2 || h[0].Version != 2 || h[1].Version != 1 {
		t.Fatalf("history = %+v, %v; want versions 2 and 1, newest first", h, err)
	}
	audit, err := store.ListAudit(ctx, pool, string(pgroup), 0, 10)
	if err != nil || len(audit) != 2 || audit[0].Action != store.AuditSettingsUpdate ||
		!strings.Contains(string(audit[1].Diff), `"key": "limits.max_bans_per_hour"`) && !strings.Contains(string(audit[1].Diff), `"key":"limits.max_bans_per_hour"`) {
		t.Fatalf("audit = %+v, %v; want one settings.update entry per save with the dotted-key diff", audit, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for count(pgroup) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("got %d notifications for %s, want one per save on %s", count(pgroup), pgroup, store.SettingsChannel)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitListening sends probe notifications until the listener reports one, so the saves that
// follow can't race the LISTEN.
func waitListening(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seen func(wa.JID) bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !seen(probe) {
		if time.Now().After(deadline) {
			t.Fatal("listener never received a probe notification")
		}
		if _, err := pool.Exec(ctx, "SELECT pg_notify($1, $2)", store.SettingsChannel, string(probe)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

const probe wa.JID = "probe@g.us"

func TestSettingsConflict(t *testing.T) {
	ctx := context.Background()
	ps := store.NewPolicyStore(storetest.Pool(t))
	doc, _ := json.Marshal(policy.Defaults())
	c := policy.SettingsChange{Group: pgroup, Doc: doc, Actor: "a"}
	if v, err := ps.SaveSettings(ctx, c); err != nil || v != 1 {
		t.Fatalf("first save = v%d, %v", v, err)
	}
	if _, err := ps.SaveSettings(ctx, c); !errors.Is(err, policy.ErrSettingsConflict) {
		t.Errorf("second first-save = %v, want ErrSettingsConflict", err)
	}
	c.PrevVersion = 1
	if v, err := ps.SaveSettings(ctx, c); err != nil || v != 2 {
		t.Fatalf("save on v1 = v%d, %v", v, err)
	}
	if _, err := ps.SaveSettings(ctx, c); !errors.Is(err, policy.ErrSettingsConflict) {
		t.Errorf("stale save on v1 = %v, want ErrSettingsConflict (optimistic concurrency)", err)
	}
	h, _ := ps.SettingsHistory(ctx, pgroup, 10)
	if len(h) != 2 {
		t.Errorf("history after a refused save = %d versions, want 2 (nothing half-written)", len(h))
	}
	if _, err := ps.SaveSettings(ctx, policy.SettingsChange{Group: pgroup, Doc: doc}); err == nil {
		t.Error("save without an actor = nil, want an error (every change is audited)")
	}
}

func TestDetectionsStore(t *testing.T) {
	ctx := context.Background()
	ps := store.NewPolicyStore(storetest.Pool(t))
	pid := int64(42)
	s := policy.Defaults()
	s.Mode = policy.ModeShadow
	f := policy.Finding{
		Band: policy.BandCertain, Source: policy.SourceKnown, Score: 0.98, Type: policy.TypeLinks,
		Signals: []policy.Signal{{Code: "url.blocklisted", Weight: 5, Known: true}},
	}
	d := policy.Decide(s, f, policy.Standing{Role: policy.RoleMember})
	rec, ok := policy.NewDetection(policy.DetectionRef{Group: pgroup, MsgID: "3EB0ABC", PersonID: &pid, TierReached: "t0", RuleVersion: "weights-1"}, f, d, policy.PlanFor(s, d))
	if !ok {
		t.Fatal("shadow spam not recorded")
	}
	got, err := ps.InsertDetection(ctx, rec)
	if err != nil || got.ID == 0 || got.CreatedAt.IsZero() {
		t.Fatalf("InsertDetection = %+v, %v", got, err)
	}
	rows, err := ps.ListDetections(ctx, pgroup, 0, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListDetections = %d rows, %v", len(rows), err)
	}
	r := rows[0]
	if r.Mode != "shadow" || r.Route != "auto_ban" || r.Verdict != "spam" || r.ConfidenceSource.String != "known" ||
		r.RuleVersion != "weights-1" || r.TierReached != "t0" || r.PersonID.Int64 != 42 ||
		!strings.Contains(string(r.Signals), "url.blocklisted") || !strings.Contains(string(r.ActionPlan), `"kind": "ban"`) {
		t.Errorf("stored detection = %+v, want shadow auto_ban spam from known with its signals, rule version and would-be plan (§7.3, §8.4)", r)
	}
	bad := rec
	bad.Score = 1.5
	if _, err := ps.InsertDetection(ctx, bad); err == nil {
		t.Error("score 1.5 stored, want refused")
	}
}

func TestGroupBansStore(t *testing.T) {
	ctx := context.Background()
	ps := store.NewPolicyStore(storetest.Pool(t))
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	c := clock.NewFake(now)
	kicks := &recordKicks{}
	bans := policy.NewBans(ps, kicks, c)

	b, err := bans.Ban(ctx, policy.BanRequest{Group: pgroup, PersonID: 7, Member: "77@lid", Reason: policy.BanSpam, DecidedBy: policy.DecidedAuto, RejoinBlock: true})
	if err != nil || !b.Active || b.ID == 0 || !b.CreatedAt.Equal(now) {
		t.Fatalf("Ban = %+v, %v", b, err)
	}
	again, err := bans.Ban(ctx, policy.BanRequest{Group: pgroup, PersonID: 7, Member: "77@lid", Reason: policy.BanSpam, DecidedBy: policy.DecidedAdmin, RejoinBlock: true})
	if err != nil || again.ID != b.ID {
		t.Fatalf("second Ban = %+v, %v; want the existing ban (one active ban per person and group)", again, err)
	}
	if kicked, err := bans.OnJoin(ctx, policy.Join{Group: pgroup, PersonID: 7, Member: "77@lid", At: now.Add(time.Hour)}); err != nil || !kicked {
		t.Fatalf("rejoin = %v, %v; want kicked", kicked, err)
	}
	if kicked, _ := bans.OnJoin(ctx, policy.Join{Group: "other@g.us", PersonID: 7, Member: "77@lid"}); kicked {
		t.Error("joining another group kicked, want a local ban to stay local")
	}
	ts, err := ps.BanTimesSince(ctx, pgroup, now.Add(-time.Hour))
	if err != nil || len(ts) != 1 || !ts[0].Equal(now) {
		t.Errorf("BanTimesSince = %v, %v; want the one ban", ts, err)
	}
	if ok, err := bans.Unban(ctx, pgroup, 7, "966500000001"); err != nil || !ok {
		t.Fatalf("Unban = %v, %v", ok, err)
	}
	if _, ok, _ := ps.ActiveBan(ctx, pgroup, 7); ok {
		t.Error("ban still active after Unban")
	}
	if kicked, _ := bans.OnJoin(ctx, policy.Join{Group: pgroup, PersonID: 7, Member: "77@lid"}); kicked {
		t.Error("rejoin after unban kicked, want allowed")
	}
	nb, err := bans.Ban(ctx, policy.BanRequest{Group: pgroup, PersonID: 7, Member: "77@lid", Reason: policy.BanOther, DecidedBy: policy.DecidedAdmin, RejoinBlock: true})
	if err != nil || nb.ID == b.ID {
		t.Fatalf("re-ban after unban = %+v, %v; want a new ban", nb, err)
	}
	if kicks.keys[0] == kicks.keys[len(kicks.keys)-1] {
		t.Errorf("re-ban reused the first ban's kick key %q; a new ban must kick again", kicks.keys[0])
	}
}

type recordKicks struct{ keys []string }

func (r *recordKicks) Kick(_ context.Context, _, _ wa.JID, _ int, key string) error {
	r.keys = append(r.keys, key)
	return nil
}
