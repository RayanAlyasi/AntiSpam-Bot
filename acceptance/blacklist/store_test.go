//go:build integration

package blacklist_test

import (
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/ruleview"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func groupAdd(t *testing.T, group wa.JID, e164, reason string, now time.Time) store.BlacklistAdd {
	t.Helper()
	return store.BlacklistAdd{Scope: rules.BlacklistGroup, GroupJID: string(group), E164: e164, Hash: hashFor(t, e164), Reason: reason, Now: now}
}

func instanceAdd(t *testing.T, e164, reason string, now time.Time) store.BlacklistAdd {
	t.Helper()
	return store.BlacklistAdd{Scope: rules.BlacklistInstance, E164: e164, Hash: hashFor(t, e164), Reason: reason, Now: now}
}

func mustAdd(t *testing.T, db *pgxpool.Pool, a store.BlacklistAdd) store.BlacklistEntry {
	t.Helper()
	e, inserted, err := store.AddBlacklist(t.Context(), db, a)
	if err != nil {
		t.Fatalf("AddBlacklist(%s %s %s): %v, want nil", a.Scope, a.GroupJID, a.E164, err)
	}
	if !inserted {
		t.Fatalf("AddBlacklist(%s %s %s) inserted = false on a fresh number, want true", a.Scope, a.GroupJID, a.E164)
	}
	return e
}

func TestStoreAddBlacklistIdempotent(t *testing.T) {
	db := storetest.Pool(t)
	ctx := t.Context()

	first, inserted, err := store.AddBlacklist(ctx, db, groupAdd(t, groupOne, listed, "crypto scam", t0))
	if err != nil || !inserted {
		t.Fatalf("first AddBlacklist = inserted %v, err %v; want inserted, nil", inserted, err)
	}
	if first.ID == 0 || first.Scope != rules.BlacklistGroup || first.GroupJID != string(groupOne) ||
		first.E164 != listed || first.Hash != hashFor(t, listed) || first.Reason != "crypto scam" ||
		!first.AddedAt.Equal(t0) {
		t.Errorf("AddBlacklist returned %+v, want the stored entry with the given scope, group, number, hash, "+
			"reason and time (every add is audited with who, when and why, DESIGN §9.8)", first)
	}

	again, inserted, err := store.AddBlacklist(ctx, db, groupAdd(t, groupOne, listed, "other reason", t0.Add(time.Hour)))
	if err != nil {
		t.Fatalf("second AddBlacklist of an active number: %v, want nil (adding twice is idempotent)", err)
	}
	if inserted {
		t.Errorf("second AddBlacklist inserted = true, want false (the number is already active in this group)")
	}
	if again.ID != first.ID || again.Reason != "crypto scam" || !again.AddedAt.Equal(t0) {
		t.Errorf("second AddBlacklist returned %+v, want the existing entry %+v unchanged", again, first)
	}

	list, err := store.ListGroupBlacklist(ctx, db, string(groupOne))
	if err != nil {
		t.Fatalf("ListGroupBlacklist: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("ListGroupBlacklist after adding the same number twice: %d entries, want 1", len(list))
	}
}

func TestStoreAddBlacklistScopes(t *testing.T) {
	db := storetest.Pool(t)
	ctx := t.Context()
	adds := []store.BlacklistAdd{
		groupAdd(t, groupOne, listed, "", t0),
		groupAdd(t, groupTwo, listed, "", t0),
		instanceAdd(t, listed, "operator", t0),
	}
	ids := map[int64]bool{}
	for _, a := range adds {
		e, inserted, err := store.AddBlacklist(ctx, db, a)
		if err != nil || !inserted {
			t.Fatalf("AddBlacklist(%s %q %s) = inserted %v, err %v; want inserted (the same number may be listed "+
				"per group and instance-wide, DESIGN §9.8)", a.Scope, a.GroupJID, a.E164, inserted, err)
		}
		ids[e.ID] = true
	}
	if len(ids) != len(adds) {
		t.Errorf("got %d distinct entry IDs for %d scopes, want one entry per scope", len(ids), len(adds))
	}
}

func TestStoreAddBlacklistRejectsInvalid(t *testing.T) {
	db := storetest.Pool(t)
	good := groupAdd(t, groupOne, listed, "", t0)
	tests := []struct {
		name string
		mod  func(a *store.BlacklistAdd)
	}{
		{"national number", func(a *store.BlacklistAdd) { a.E164 = "0551234567" }},
		{"no plus", func(a *store.BlacklistAdd) { a.E164 = "966551234567" }},
		{"spaces", func(a *store.BlacklistAdd) { a.E164 = "+966 55 123 4567" }},
		{"empty number", func(a *store.BlacklistAdd) { a.E164 = "" }},
		{"non-hex hash", func(a *store.BlacklistAdd) { a.Hash = "not-a-hex-hash" }},
		{"group entry without group", func(a *store.BlacklistAdd) { a.GroupJID = "" }},
		{"instance entry with group", func(a *store.BlacklistAdd) { a.Scope = rules.BlacklistInstance }},
		{"unknown scope", func(a *store.BlacklistAdd) { a.Scope = rules.BlacklistScope("global") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := good
			tt.mod(&a)
			if _, _, err := store.AddBlacklist(t.Context(), db, a); err == nil {
				t.Errorf("AddBlacklist(%+v) error = nil, want an error (invalid entries must never reach the table)", a)
			}
		})
	}
	list, err := store.ListGroupBlacklist(t.Context(), db, string(groupOne))
	if err != nil {
		t.Fatalf("ListGroupBlacklist: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("ListGroupBlacklist after only invalid adds: %+v, want empty", list)
	}
}

func TestStoreRemoveAndReAdd(t *testing.T) {
	db := storetest.Pool(t)
	ctx := t.Context()
	first := mustAdd(t, db, groupAdd(t, groupOne, listed, "spam", t0))
	inst := mustAdd(t, db, instanceAdd(t, listed, "operator", t0))

	removed, err := store.RemoveBlacklist(ctx, db, rules.BlacklistGroup, string(groupOne), listed, nil, t0.Add(time.Minute))
	if err != nil || !removed {
		t.Fatalf("RemoveBlacklist of an active entry = %v, %v; want true, nil", removed, err)
	}
	removed, err = store.RemoveBlacklist(ctx, db, rules.BlacklistGroup, string(groupOne), listed, nil, t0.Add(2*time.Minute))
	if err != nil || removed {
		t.Errorf("RemoveBlacklist again = %v, %v; want false, nil (nothing active is left to remove)", removed, err)
	}
	removed, err = store.RemoveBlacklist(ctx, db, rules.BlacklistGroup, string(groupTwo), unlisted, nil, t0)
	if err != nil || removed {
		t.Errorf("RemoveBlacklist of a number never listed = %v, %v; want false, nil", removed, err)
	}

	list, err := store.ListGroupBlacklist(ctx, db, string(groupOne))
	if err != nil {
		t.Fatalf("ListGroupBlacklist: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("ListGroupBlacklist after remove = %+v, want empty (removed entries are no longer listed)", list)
	}
	check, err := store.CheckBlacklist(ctx, db, string(groupOne), listed)
	if err != nil {
		t.Fatalf("CheckBlacklist: %v", err)
	}
	if len(check) != 1 || check[0].ID != inst.ID {
		t.Errorf("CheckBlacklist after removing the group entry = %+v, want only the instance entry %d "+
			"(a group remove doesn't touch the instance list)", check, inst.ID)
	}

	readded, inserted, err := store.AddBlacklist(ctx, db, groupAdd(t, groupOne, listed, "back again", t0.Add(time.Hour)))
	if err != nil || !inserted {
		t.Fatalf("AddBlacklist after remove = inserted %v, err %v; want inserted (a removed number can be listed again)", inserted, err)
	}
	if readded.ID == first.ID {
		t.Errorf("re-added entry reuses ID %d, want a new entry (the removed one stays as history for the audit)", first.ID)
	}
}

func TestStoreListGroupNewestFirst(t *testing.T) {
	db := storetest.Pool(t)
	a := mustAdd(t, db, groupAdd(t, groupOne, listed, "first", t0))
	b := mustAdd(t, db, groupAdd(t, groupOne, unlisted, "second", t0.Add(time.Minute)))
	c := mustAdd(t, db, groupAdd(t, groupOne, listedOther, "third", t0.Add(2*time.Minute)))
	mustAdd(t, db, groupAdd(t, groupTwo, nearMiss, "other group", t0.Add(3*time.Minute)))

	list, err := store.ListGroupBlacklist(t.Context(), db, string(groupOne))
	if err != nil {
		t.Fatalf("ListGroupBlacklist: %v", err)
	}
	var got []int64
	for _, e := range list {
		got = append(got, e.ID)
	}
	if want := []int64{c.ID, b.ID, a.ID}; !slices.Equal(got, want) {
		t.Errorf("ListGroupBlacklist IDs = %v, want %v (this group's entries only, newest first, DESIGN §9.8)", got, want)
	}
}

func TestStoreCheckBlacklist(t *testing.T) {
	db := storetest.Pool(t)
	own := mustAdd(t, db, groupAdd(t, groupOne, listed, "", t0))
	inst := mustAdd(t, db, instanceAdd(t, listed, "", t0))
	mustAdd(t, db, groupAdd(t, groupTwo, listed, "", t0))
	instOnly := mustAdd(t, db, instanceAdd(t, listedOther, "", t0))

	ids := func(es []store.BlacklistEntry) []int64 {
		var out []int64
		for _, e := range es {
			out = append(out, e.ID)
		}
		slices.Sort(out)
		return out
	}
	sorted := func(v ...int64) []int64 { slices.Sort(v); return v }

	tests := []struct {
		name string
		e164 string
		want []int64
	}{
		{"own and instance", listed, sorted(own.ID, inst.ID)},
		{"instance only", listedOther, sorted(instOnly.ID)},
		{"not listed", unlisted, nil},
	}
	for _, tt := range tests {
		got, err := store.CheckBlacklist(t.Context(), db, string(groupOne), tt.e164)
		if err != nil {
			t.Fatalf("CheckBlacklist(%s): %v", tt.e164, err)
		}
		if !slices.Equal(ids(got), tt.want) {
			t.Errorf("%s: CheckBlacklist(groupOne, %s) IDs = %v, want %v (the group's own and the instance-wide "+
				"active entries, never another group's, DESIGN §9.8)", tt.name, tt.e164, ids(got), tt.want)
		}
	}
}

func TestStoreRulesLoaderActiveOnly(t *testing.T) {
	db := storetest.Pool(t)
	g1 := mustAdd(t, db, groupAdd(t, groupOne, listed, "", t0))
	g2 := mustAdd(t, db, groupAdd(t, groupTwo, listedOther, "", t0))
	in := mustAdd(t, db, instanceAdd(t, unlisted, "", t0))
	mustAdd(t, db, groupAdd(t, groupOne, nearMiss, "", t0))
	if ok, err := store.RemoveBlacklist(t.Context(), db, rules.BlacklistGroup, string(groupOne), nearMiss, nil, t0); err != nil || !ok {
		t.Fatalf("RemoveBlacklist = %v, %v; want true, nil", ok, err)
	}

	got, err := store.RulesLoader{Q: db}.LoadBlacklist(t.Context())
	if err != nil {
		t.Fatalf("LoadBlacklist: %v", err)
	}
	want := map[int64]rules.BlacklistEntry{
		g1.ID: {ID: g1.ID, Scope: rules.BlacklistGroup, GroupJID: string(groupOne), PNHash: hashFor(t, listed)},
		g2.ID: {ID: g2.ID, Scope: rules.BlacklistGroup, GroupJID: string(groupTwo), PNHash: hashFor(t, listedOther)},
		in.ID: {ID: in.ID, Scope: rules.BlacklistInstance, PNHash: hashFor(t, unlisted)},
	}
	if len(got) != len(want) {
		t.Errorf("LoadBlacklist returned %d entries %+v, want %d (only active entries; a removed one must not "+
			"keep matching content)", len(got), got, len(want))
	}
	for _, e := range got {
		if w, ok := want[e.ID]; !ok || e != w {
			t.Errorf("LoadBlacklist entry %+v, want one of %+v", e, want)
		}
	}
}

func TestStoreBlacklistEndToEnd(t *testing.T) {
	db := storetest.Pool(t)
	ctx := t.Context()
	h := newHasher(t)
	holder := rules.NewHolder(store.RulesLoader{Q: db}, clock.Real{}, zerolog.Nop())
	reload := func() {
		t.Helper()
		if err := holder.Reload(ctx); err != nil {
			t.Fatalf("Holder.Reload over the store: %v, want nil", err)
		}
	}
	a := blacklist.New[policy](ruleview.Source{
		Rules: holder,
		Settings: func(e *envelope.Envelope) rules.ViewOptions {
			return rules.ViewOptions{Bot: string(e.Bot), Group: string(e.Group), UseInstanceBlacklist: true}
		},
	}, h.Hash)
	vcard := func(waid string) *envelope.Envelope {
		return vcardEnvelope(groupOne, envelope.ContactRef{DisplayName: "Offers", WAIDs: []string{waid}})
	}

	parsed := blacklist.ParseArgs("0551234567 wa.me/447911123456 fake investments", region)
	for _, n := range parsed.Numbers {
		if _, _, err := store.AddBlacklist(ctx, db, store.BlacklistAdd{
			Scope: rules.BlacklistGroup, GroupJID: string(groupOne), E164: n, Hash: h.Hash(n), Reason: parsed.Reason, Now: t0,
		}); err != nil {
			t.Fatalf("AddBlacklist(%s): %v", n, err)
		}
	}
	reload()
	for _, n := range []string{listed, listedOther} {
		checkKnown(t, "vcard waid "+n, n, analyze(t, a, vcard(n[1:])))
	}

	if ok, err := store.RemoveBlacklist(ctx, db, rules.BlacklistGroup, string(groupOne), listed, nil, t0.Add(time.Hour)); err != nil || !ok {
		t.Fatalf("RemoveBlacklist = %v, %v; want true, nil", ok, err)
	}
	reload()
	if sigs := blacklistSignals(analyze(t, a, vcard("966551234567"))); len(sigs) != 0 {
		t.Errorf("after .blacklist remove and a reload: got %+v, want no signal (remove lifts the content signal, "+
			"DESIGN §9.8 step 5)", sigs)
	}
	if sigs := blacklistSignals(analyze(t, a, vcard("447911123456"))); len(sigs) != 1 {
		t.Errorf("the other number still listed: got %d signals, want 1 (remove only lifts the removed number)", len(sigs))
	}

	if _, _, err := store.AddBlacklist(ctx, db, instanceAdd(t, unlisted, "operator", t0)); err != nil {
		t.Fatalf("AddBlacklist(instance): %v", err)
	}
	reload()
	if sigs := blacklistSignals(analyze(t, a, bodyEnvelope(groupOne, "call 0501234567"))); len(sigs) != 1 {
		t.Errorf("instance entry in a group that uses the instance list: got %d signals, want 1", len(sigs))
	}
}
