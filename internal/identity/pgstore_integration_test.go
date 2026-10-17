//go:build integration

package identity

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

func pgService(t *testing.T, pool *pgxpool.Pool) (*Service, *recEvents, *clock.Fake) {
	t.Helper()
	ev := &recEvents{}
	clk := clock.NewFake(t0)
	return New(NewPostgresStore(pool), nil, ev, clk), ev, clk
}

func TestPostgresMatchLearnAndRecycle(t *testing.T) {
	pool := storetest.Pool(t)
	svc, ev, clk := pgService(t, pool)
	ctx := t.Context()

	pnOnly, err := svc.Ensure(ctx, "966501112233:3@s.whatsapp.net", SourceEvent)
	if err != nil || pnOnly.PN != "966501112233@s.whatsapp.net" || pnOnly.LID != "" {
		t.Fatalf("Ensure PN = %+v, %v; want a PN-only person", pnOnly, err)
	}
	if again, _ := svc.Ensure(ctx, "966501112233@s.whatsapp.net", SourceEvent); again.ID != pnOnly.ID {
		t.Fatalf("Ensure twice: %d then %d, want one person", pnOnly.ID, again.ID)
	}
	p, err := svc.Learn(ctx, "77@lid", "966501112233@s.whatsapp.net", SourceSenderAlt)
	if err != nil || p.ID != pnOnly.ID || p.LID != "77@lid" {
		t.Fatalf("Learn = %+v, %v; want the PN-only record to gain its LID", p, err)
	}
	ids, _ := svc.Identities(ctx, p.ID)
	if len(ids) != 2 || ids[0].CountryCode != "966" || ids[1].Source != SourceSenderAlt {
		t.Errorf("identities %+v: want the PN row with country code and the LID row with its source", ids)
	}
	var prefix string
	if err := pool.QueryRow(ctx, "SELECT number_prefix FROM app.identities WHERE kind='pn'").Scan(&prefix); err != nil || prefix != "96650" {
		t.Errorf("number_prefix = %q, %v; want 96650", prefix, err)
	}

	clk.Advance(time.Hour)
	if _, err := svc.Learn(ctx, "88@lid", "966501112233@s.whatsapp.net", SourceSenderAlt); err != nil {
		t.Fatal(err)
	}
	old, _, _ := svc.MatchPerson(ctx, "77@lid")
	nw, _, _ := svc.MatchPerson(ctx, "966501112233@s.whatsapp.net")
	if old.ID == nw.ID || old.PN != "" || nw.LID != "88@lid" {
		t.Errorf("after recycle: old %+v, new %+v; want separate persons, the number with the new LID only", old, nw)
	}
	var closed int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM app.identities WHERE value = '966501112233@s.whatsapp.net' AND valid_to IS NOT NULL").Scan(&closed)
	if closed != 1 || len(ev.recycled) != 1 {
		t.Errorf("closed links %d, recycled events %d; want 1 and 1 (old link kept with valid_to)", closed, len(ev.recycled))
	}
}

func TestPostgresMergeRepointsReferencingTables(t *testing.T) {
	pool := storetest.Pool(t)
	ctx := t.Context()
	// A table a later feature might add: one row per person, unique per person and group.
	_, err := pool.Exec(ctx, `CREATE TABLE app.test_strikes (
		id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		person_id bigint NOT NULL REFERENCES app.persons (id),
		group_jid text NOT NULL,
		UNIQUE (person_id, group_jid))`)
	if err != nil {
		t.Fatal(err)
	}
	// And one created before app.persons existed, so it has no foreign key (like detections).
	if _, err := pool.Exec(ctx, `CREATE TABLE app.test_decisions (id bigint, decider_person bigint)`); err != nil {
		t.Fatal(err)
	}
	svc, ev, _ := pgService(t, pool)
	g1, g2 := wa.JID("1@g.us"), wa.JID("2@g.us")
	pnOnly, _ := svc.Ensure(ctx, "966502223344@s.whatsapp.net", SourceEvent)
	lidP, _ := svc.Ensure(ctx, "99@lid", SourceEvent)
	_ = svc.MemberJoined(ctx, g1, pnOnly.ID, t0)
	_ = svc.CountMessage(ctx, g1, pnOnly.ID, true)
	_ = svc.SeedMember(ctx, g1, lidP.ID, RoleAdmin, false)
	_ = svc.AddMember(ctx, g2, pnOnly.ID)
	for _, row := range []struct {
		p PersonID
		g string
	}{{pnOnly.ID, "1@g.us"}, {pnOnly.ID, "2@g.us"}, {lidP.ID, "1@g.us"}} {
		if _, err := pool.Exec(ctx, "INSERT INTO app.test_strikes (person_id, group_jid) VALUES ($1, $2)", row.p, row.g); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := pool.Exec(ctx, "INSERT INTO app.test_decisions VALUES (1, $1)", pnOnly.ID); err != nil {
		t.Fatal(err)
	}

	p, err := svc.Learn(ctx, "99@lid", "966502223344@s.whatsapp.net", SourceSenderAlt)
	if err != nil || p.ID != lidP.ID {
		t.Fatalf("Learn = %+v, %v; want the LID person", p, err)
	}
	if len(ev.resolved) != 1 || ev.resolved[0].MergedFrom != pnOnly.ID {
		t.Fatalf("resolved %+v: want one event with MergedFrom = %d", ev.resolved, pnOnly.ID)
	}
	groups, _ := svc.GroupsOf(ctx, lidP.ID)
	m, _, _ := svc.Member(ctx, g1, lidP.ID)
	if len(groups) != 2 || m.Role != RoleAdmin || m.MsgCount != 1 || !m.JoinedAt.Equal(t0) {
		t.Errorf("after merge: groups %v, member %+v; want both groups and the combined g1 membership", groups, m)
	}
	var strikes, left int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM app.test_strikes WHERE person_id = $1", lidP.ID).Scan(&strikes)
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM app.test_strikes WHERE person_id = $1", pnOnly.ID).Scan(&left)
	if strikes != 2 || left != 0 {
		t.Errorf("test_strikes: %d on the survivor, %d left on the merged record; want 2 and 0 (the g1 duplicate dropped)", strikes, left)
	}
	var decider int64
	_ = pool.QueryRow(ctx, "SELECT decider_person FROM app.test_decisions").Scan(&decider)
	if decider != int64(lidP.ID) {
		t.Errorf("person column without a foreign key: %d, want re-pointed to %d", decider, lidP.ID)
	}
	var merges, audits int
	var mergedInto int64
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM app.person_merges WHERE from_person = $1 AND into_person = $2", pnOnly.ID, lidP.ID).Scan(&merges)
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM app.audit_log WHERE action = 'identity.merge'").Scan(&audits)
	_ = pool.QueryRow(ctx, "SELECT merged_into FROM app.persons WHERE id = $1", pnOnly.ID).Scan(&mergedInto)
	if merges != 1 || audits != 1 || mergedInto != int64(lidP.ID) {
		t.Errorf("person_merges %d, audit rows %d, merged_into %d; want 1, 1 and %d", merges, audits, mergedInto, lidP.ID)
	}
	if _, err := svc.Person(ctx, pnOnly.ID); err == nil {
		t.Error("merged-away person still readable")
	}
}

func TestPostgresConcurrentProcessesCreateOnePerson(t *testing.T) {
	pool := storetest.Pool(t)
	ctx := context.Background()
	// Two Services stand for two processes: only the database serializes them.
	a, _, _ := pgService(t, pool)
	b, _, _ := pgService(t, pool)
	var wg sync.WaitGroup
	ids := make([]PersonID, 16)
	for i := range ids {
		svc := a
		if i%2 == 1 {
			svc = b
		}
		wg.Go(func() {
			p, err := svc.Learn(ctx, "55@lid", "966503334455@s.whatsapp.net", SourceSenderAlt)
			if err != nil {
				t.Error(err)
			}
			ids[i] = p.ID
		})
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("ids %v: want one person for one pair across processes", ids)
		}
	}
}

func TestPostgresGroupStore(t *testing.T) {
	pool := storetest.Pool(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, "INSERT INTO app.bots (jid) VALUES ('966500000000@s.whatsapp.net')"); err != nil {
		t.Fatal(err)
	}
	gs := NewPostgresGroupStore(pool)
	g := Group{
		JID: "120@g.us", Bot: "966500000000@s.whatsapp.net", Name: "Cars", Size: 3, AddressingMode: "lid",
		BotIsAdmin: true, Status: GroupActive, JoinedAt: t0, LastInfoRefresh: t0,
	}
	if err := gs.UpsertGroup(ctx, g); err != nil {
		t.Fatal(err)
	}
	got, ok, err := gs.Group(ctx, g.JID)
	if err != nil || !ok || got.Bot != g.Bot || got.Name != "Cars" || !got.JoinedAt.Equal(t0) || got.Status != GroupActive {
		t.Fatalf("Group = %+v %v %v, want the stored row", got, ok, err)
	}
	stale, _ := gs.StaleGroups(ctx, t0)
	fresh, _ := gs.StaleGroups(ctx, t0.Add(-time.Second))
	if len(stale) != 1 || len(fresh) != 0 {
		t.Errorf("stale: %d by t0, %d before; want 1 and 0", len(stale), len(fresh))
	}
	g.Status = GroupLeft
	_ = gs.UpsertGroup(ctx, g)
	if s, _ := gs.StaleGroups(ctx, t0.Add(time.Hour)); len(s) != 0 {
		t.Error("left groups are never refreshed")
	}
	if all, _ := gs.Groups(ctx); len(all) != 1 {
		t.Errorf("Groups() = %d, want all groups", len(all))
	}
	if act, _ := gs.Groups(ctx, GroupActive); len(act) != 0 {
		t.Errorf("Groups(active) = %d, want 0", len(act))
	}
}
