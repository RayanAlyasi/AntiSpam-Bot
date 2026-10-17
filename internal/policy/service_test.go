package policy

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const sgroup wa.JID = "1203@g.us"

func TestSettingsServiceDefaultsThenVersions(t *testing.T) {
	ctx := context.Background()
	repo := NewMemSettingsRepo(clock.NewFake(t0))
	svc := NewSettingsService(repo)

	s, v, err := svc.Get(ctx, sgroup)
	if err != nil || v != 0 || s.Preset != PresetStandard {
		t.Fatalf("Get of a new group = %s v%d, %v; want the defaults at version 0", s.Preset, v, err)
	}
	s, changes, err := svc.Set(ctx, sgroup, "966500000001", "enforcement.autoban_matrix.videos.action", json.RawMessage(`"ask"`))
	if err != nil {
		t.Fatal(err)
	}
	if s.Enforcement.AutobanMatrix[TypeVideos].Action != CellAsk || len(changes) == 0 {
		t.Fatalf("Set = %v, %v; want videos ask and the changes", s.Enforcement.AutobanMatrix[TypeVideos], changes)
	}
	if _, _, err := svc.Set(ctx, sgroup, "966500000001", "limits.max_bans_per_hour", json.RawMessage(`5`)); err != nil {
		t.Fatal(err)
	}
	h := repo.History(sgroup)
	if len(h) != 2 || h[0].Version != 1 || h[1].Version != 2 || h[1].UpdatedBy != "966500000001" {
		t.Fatalf("history = %+v, want versions 1 and 2 by the actor (§14 group_settings_history)", h)
	}
	audit := repo.Audit()
	if len(audit) != 2 || !strings.Contains(string(audit[1].Diff), `"key":"limits.max_bans_per_hour","from":10,"to":5`) ||
		!strings.Contains(string(audit[1].Diff), `"version":2`) {
		t.Fatalf("audit = %+v, want each change with its dotted key, old and new value and version", audit)
	}
	if !slices.Equal(repo.Notified(), []wa.JID{sgroup, sgroup}) {
		t.Errorf("notified %v, want one notification per save", repo.Notified())
	}
	s, v, _ = svc.Get(ctx, sgroup)
	if v != 2 || s.Limits.MaxBansPerHour != 5 {
		t.Errorf("Get after two saves = v%d cap %d, want v2 cap 5", v, s.Limits.MaxBansPerHour)
	}
}

func TestSettingsServiceRejectsAndNoops(t *testing.T) {
	ctx := context.Background()
	repo := NewMemSettingsRepo(clock.NewFake(t0))
	svc := NewSettingsService(repo)
	if _, _, err := svc.Set(ctx, sgroup, "a", "review.on_timeout", json.RawMessage(`"auto_ban"`)); err != nil {
		t.Fatal(err) // allowed in auto mode
	}
	if _, _, err := svc.Set(ctx, sgroup, "a", "enforcement.mode", json.RawMessage(`"ask_delete_first"`)); err == nil {
		t.Error("ask mode with an auto_ban timeout: saved, want refused (ask modes never ban)")
	}
	if _, _, err := svc.Set(ctx, sgroup, "a", "colour", json.RawMessage(`"red"`)); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("unknown key: %v, want ErrUnknownKey", err)
	}
	if _, _, err := svc.Set(ctx, sgroup, "", "silent", json.RawMessage(`false`)); err == nil {
		t.Error("no actor: saved, want refused (every change is audited)")
	}
	n := len(repo.History(sgroup))
	if _, changes, err := svc.Set(ctx, sgroup, "a", "silent", json.RawMessage(`true`)); err != nil || changes != nil || len(repo.History(sgroup)) != n {
		t.Errorf("setting the current value: changes %v, %v, %d versions; want nothing stored", changes, err, len(repo.History(sgroup)))
	}
}

func TestSettingsServiceConflictRetries(t *testing.T) {
	ctx := context.Background()
	repo := NewMemSettingsRepo(clock.NewFake(t0))
	svc := NewSettingsService(repo)
	other, _ := PresetSettings(PresetStrict)
	otherDoc, _ := json.Marshal(other)
	once := true
	repo.OnSave = func(g wa.JID) {
		if once {
			once = false
			repo.Put(g, otherDoc, "website")
		}
	}
	s, _, err := svc.Set(ctx, sgroup, "a", "limits.max_bans_per_hour", json.RawMessage(`3`))
	if err != nil {
		t.Fatal(err)
	}
	if s.Preset != PresetStrict || s.Limits.MaxBansPerHour != 3 {
		t.Errorf("after a concurrent change: preset %s cap %d, want the key re-applied on the newer document (strict, 3)", s.Preset, s.Limits.MaxBansPerHour)
	}
	repo.OnSave = func(g wa.JID) { repo.Put(g, otherDoc, "website") }
	if _, _, err := svc.Set(ctx, sgroup, "a", "limits.max_bans_per_hour", json.RawMessage(`4`)); !errors.Is(err, ErrSettingsConflict) {
		t.Errorf("endless conflicts: %v, want ErrSettingsConflict after a few tries", err)
	}
}

func TestSettingsServiceCacheAndInvalidate(t *testing.T) {
	ctx := context.Background()
	repo := NewMemSettingsRepo(clock.NewFake(t0))
	svc := NewSettingsService(repo)
	_, _, _ = svc.Get(ctx, sgroup)
	paused := Defaults()
	paused.Mode = ModePaused
	doc, _ := json.Marshal(paused)
	repo.Put(sgroup, doc, "other-process")
	if s, _, _ := svc.Get(ctx, sgroup); s.Mode != ModeEnforce {
		t.Fatalf("cached Get = %s, want the cached enforce until invalidated", s.Mode)
	}
	svc.Invalidate(sgroup)
	if s, v, _ := svc.Get(ctx, sgroup); s.Mode != ModePaused || v != 1 {
		t.Errorf("Get after Invalidate = %s v%d, want the stored paused v1 (NOTIFY refresh)", s.Mode, v)
	}
	// The cached copy can't be changed through what Get returns.
	s, _, _ := svc.Get(ctx, sgroup)
	s.Enforcement.AutobanMatrix[TypeText] = Cell{Action: CellAllow}
	if s2, _, _ := svc.Get(ctx, sgroup); s2.Enforcement.AutobanMatrix[TypeText].Action == CellAllow {
		t.Error("changing a returned document changed the cache")
	}
	repo.Put("bad@g.us", json.RawMessage(`{"mode":"loud"}`), "x")
	if _, _, err := svc.Get(ctx, "bad@g.us"); err == nil {
		t.Error("invalid stored document: Get = nil, want an error")
	}
}

func TestSettingsServicePresetAndReplace(t *testing.T) {
	ctx := context.Background()
	svc := NewSettingsService(NewMemSettingsRepo(clock.NewFake(t0)))
	s, changes, err := svc.ApplyPreset(ctx, sgroup, "a", PresetAskAdmins)
	if err != nil || s.Enforcement.Mode != EnforceAskDeleteFirst || len(changes) == 0 {
		t.Fatalf("ApplyPreset(ask_admins) = %s, %d changes, %v", s.Enforcement.Mode, len(changes), err)
	}
	if _, _, err := svc.ApplyPreset(ctx, sgroup, "a", "chaos"); err == nil {
		t.Error("unknown preset accepted")
	}
	bad := Defaults()
	bad.Mode = "loud"
	if _, _, err := svc.Replace(ctx, sgroup, "a", bad, 1); err == nil {
		t.Error("Replace with an invalid document: nil, want an error")
	}
	good := Defaults()
	good.Silent = false
	if s, _, err := svc.Replace(ctx, sgroup, "a", good, 1); err != nil || s.Silent || s.Preset != PresetStandard {
		t.Errorf("Replace = %+v, %v", s.Silent, err)
	}
	if _, _, err := svc.Replace(ctx, sgroup, "a", Defaults(), 1); !errors.Is(err, ErrSettingsConflict) {
		t.Errorf("Replace from an old version = %v, want ErrSettingsConflict (an edit never reverts another change)", err)
	}
}

func TestDetectionRecord(t *testing.T) {
	pid := int64(7)
	ref := DetectionRef{Group: sgroup, MsgID: "ABC", PersonID: &pid, TierReached: "t0", RuleVersion: "w1"}
	shadow := Defaults()
	shadow.Mode = ModeShadow
	f := Finding{Band: BandCertain, Source: SourceKnown, Score: 0.99, Type: TypeText, Signals: []Signal{{Code: "url.blocklisted", Weight: 5, Known: true}}}
	d := Decide(shadow, f, Standing{Role: RoleMember})
	out := Outcome{Finding: f, Decision: d, Plan: PlanFor(shadow, d)}
	r, ok := out.Record(ref)
	if !ok || r.Mode != DetectionShadow || r.Route != RouteAutoBan || r.Verdict != VerdictSpam || r.Source != SourceKnown ||
		len(r.ActionPlan) != 2 || r.RuleVersion != "w1" || r.Score != 0.99 || r.Reason == "" || *r.PersonID != 7 {
		t.Fatalf("shadow record = %+v, want the would-be delete and ban with mode shadow (§8.4)", r)
	}
	if _, ok := NewDetection(ref, Finding{Band: BandClean}, Decision{Verdict: VerdictClean, Route: RouteNone}, Plan{}); ok {
		t.Error("clean message recorded, want nothing (only non-clean verdicts are stored)")
	}
}

func TestSettingsServiceSaveReappliesChanges(t *testing.T) {
	ctx := context.Background()
	repo := NewMemSettingsRepo(clock.NewFake(t0))
	svc := NewSettingsService(repo)
	loaded, err := svc.Load(ctx, sgroup)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := loaded.Set("limits.max_bans_per_hour", json.RawMessage(`2`))
	changes, _ := Diff(loaded, next)
	// Someone else switches the group to shadow mode meanwhile.
	shadow := Defaults()
	shadow.Mode = ModeShadow
	doc, _ := json.Marshal(shadow)
	repo.Put(sgroup, doc, "website")
	if err := svc.Save(ctx, sgroup, next, "1@lid", changes); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Load(ctx, sgroup)
	if got.Mode != ModeShadow || got.Limits.MaxBansPerHour != 2 {
		t.Errorf("after Save: mode %s cap %d, want both the concurrent shadow mode and the saved cap 2", got.Mode, got.Limits.MaxBansPerHour)
	}
	if a := repo.Audit(); len(a) != 1 || a[0].Actor != "1@lid" {
		t.Errorf("audit = %+v, want one entry by the saving admin", a)
	}
}

func TestSettingsServiceSavePresetSwitches(t *testing.T) {
	ctx := context.Background()
	presets := []Preset{PresetRelaxed, PresetStandard, PresetStrict, PresetAskAdmins}
	for _, from := range presets {
		for _, to := range presets {
			svc := NewSettingsService(NewMemSettingsRepo(clock.NewFake(t0)))
			if _, _, err := svc.ApplyPreset(ctx, sgroup, "a", from); err != nil {
				t.Fatal(err)
			}
			cur, _ := svc.Load(ctx, sgroup)
			next := cur.Clone()
			if err := next.ApplyPreset(to); err != nil {
				t.Fatal(err)
			}
			changes, _ := Diff(cur, next)
			if err := svc.Save(ctx, sgroup, next, "1@lid", changes); err != nil {
				t.Errorf("%s → %s through Save: %v, want the switch stored (the .preset command)", from, to, err)
				continue
			}
			got, _ := svc.Load(ctx, sgroup)
			if d, _ := Diff(next, got); len(d) != 0 {
				t.Errorf("%s → %s: stored document differs from the switched one: %v", from, to, d)
			}
		}
	}
}

func TestSettingsServiceSaveMultiKeyThroughInvalidState(t *testing.T) {
	ctx := context.Background()
	svc := NewSettingsService(NewMemSettingsRepo(clock.NewFake(t0)))
	if _, _, err := svc.Set(ctx, sgroup, "a", "review.on_timeout", json.RawMessage(`"auto_ban"`)); err != nil {
		t.Fatal(err)
	}
	cur, _ := svc.Load(ctx, sgroup)
	next := cur.Clone()
	next.Enforcement.Mode = EnforceAskDeleteFirst
	next.Review.OnTimeout = TimeoutKeepDeletedNoBan
	next.Detection.Thresholds = Thresholds{Review: 0.3, Delete: 0.4, Ban: 0.5}
	changes, _ := Diff(cur, next)
	if err := svc.Save(ctx, sgroup, next, "1@lid", changes); err != nil {
		t.Fatalf("valid multi-key change refused: %v (keys must apply together)", err)
	}
	got, _ := svc.Load(ctx, sgroup)
	if got.Enforcement.Mode != EnforceAskDeleteFirst || got.Detection.Thresholds.Ban != 0.5 {
		t.Errorf("stored = %s %+v, want the whole change", got.Enforcement.Mode, got.Detection.Thresholds)
	}
}

func TestSettingsServiceSaveWithoutChangesStoresNothing(t *testing.T) {
	ctx := context.Background()
	repo := NewMemSettingsRepo(clock.NewFake(t0))
	svc := NewSettingsService(repo)
	old, _ := svc.Load(ctx, sgroup)
	if _, _, err := svc.Set(ctx, sgroup, "b", "silent", json.RawMessage(`false`)); err != nil {
		t.Fatal(err)
	}
	if err := svc.Save(ctx, sgroup, old, "1@lid", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Load(ctx, sgroup); got.Silent {
		t.Error("Save with no changes reverted another admin's change")
	}
}

type racingRepo struct {
	*MemSettingsRepo
	beforeReturn func()
}

func (r *racingRepo) LoadSettings(ctx context.Context, g wa.JID) (StoredSettings, bool, error) {
	s, ok, err := r.MemSettingsRepo.LoadSettings(ctx, g)
	if r.beforeReturn != nil {
		f := r.beforeReturn
		r.beforeReturn = nil
		f()
	}
	return s, ok, err
}

func TestSettingsServiceStaleLoadNeverCached(t *testing.T) {
	ctx := context.Background()
	mem := NewMemSettingsRepo(clock.NewFake(t0))
	repo := &racingRepo{MemSettingsRepo: mem}
	svc := NewSettingsService(repo)
	askB := Defaults()
	askB.Enforcement.Mode = EnforceAskBeforeAction
	doc, _ := json.Marshal(askB)
	// The load reads version 0; before it returns, another process saves ask_before_action
	// and the NOTIFY invalidates the group.
	repo.beforeReturn = func() {
		mem.Put(sgroup, doc, "website")
		svc.Invalidate(sgroup)
	}
	if _, _, err := svc.Get(ctx, sgroup); err != nil {
		t.Fatal(err)
	}
	if s, v, _ := svc.Get(ctx, sgroup); s.Enforcement.Mode != EnforceAskBeforeAction || v != 1 {
		t.Errorf("after a racing save and invalidation: %s v%d, want ask_before_action v1 (a stale load must not be cached)", s.Enforcement.Mode, v)
	}
	svc.InvalidateAll()
	if s, _, _ := svc.Get(ctx, sgroup); s.Enforcement.Mode != EnforceAskBeforeAction {
		t.Error("InvalidateAll lost the document")
	}
}
