package policy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

type memBans struct {
	bans []GroupBan
	err  error
}

func (m *memBans) CreateBan(_ context.Context, b GroupBan) (GroupBan, bool, error) {
	if m.err != nil {
		return GroupBan{}, false, m.err
	}
	for _, x := range m.bans {
		if x.Active && x.Group == b.Group && x.PersonID == b.PersonID {
			return x, false, nil
		}
	}
	b.ID = int64(len(m.bans) + 1)
	m.bans = append(m.bans, b)
	return b, true, nil
}

func (m *memBans) ActiveBan(_ context.Context, g wa.JID, p int64) (GroupBan, bool, error) {
	for _, x := range m.bans {
		if x.Active && x.Group == g && x.PersonID == p {
			return x, true, nil
		}
	}
	return GroupBan{}, false, m.err
}

func (m *memBans) LiftBan(_ context.Context, g wa.JID, p int64, by string, at time.Time) (bool, error) {
	for i, x := range m.bans {
		if x.Active && x.Group == g && x.PersonID == p {
			m.bans[i].Active, m.bans[i].LiftedBy, m.bans[i].LiftedAt = false, by, at
			return true, nil
		}
	}
	return false, nil
}

type kick struct {
	group, member wa.JID
	prio          int
	key           string
}

type fakeKicker struct{ kicks []kick }

func (f *fakeKicker) Kick(_ context.Context, g, m wa.JID, prio int, key string) error {
	f.kicks = append(f.kicks, kick{g, m, prio, key})
	return nil
}

const (
	grp    wa.JID = "1203@g.us"
	other  wa.JID = "9999@g.us"
	member wa.JID = "77@lid"
)

func TestLocalBan(t *testing.T) {
	ctx := context.Background()
	c := clock.NewFake(t0)
	store, kicker := &memBans{}, &fakeKicker{}
	b := NewBans(store, kicker, c)

	ban, err := b.Ban(ctx, BanRequest{Group: grp, PersonID: 7, Member: member, Reason: BanSpam, DecidedBy: DecidedAuto, RejoinBlock: true})
	if err != nil {
		t.Fatal(err)
	}
	if !ban.Active || !ban.RejoinBlock || ban.Reason != BanSpam || !ban.CreatedAt.Equal(t0) {
		t.Errorf("ban = %+v, want an active spam ban with a rejoin block", ban)
	}
	if len(kicker.kicks) != 1 || kicker.kicks[0].prio != P2 || kicker.kicks[0].key != "kick:1203@g.us:77@lid:ban1" {
		t.Fatalf("kicks = %+v, want one P2 kick with the kick dedupe key (§10.2)", kicker.kicks)
	}
	if _, err := b.Ban(ctx, BanRequest{Group: grp, PersonID: 7, Member: member, Reason: BanSpam, DecidedBy: DecidedAdmin, RejoinBlock: true}); err != nil {
		t.Fatal(err)
	}
	if len(store.bans) != 1 {
		t.Errorf("banning twice recorded %d bans, want 1", len(store.bans))
	}

	// Rejoining this group: kicked at P0. Joining another group: untouched (local ban).
	c.Advance(time.Hour)
	if kicked, err := b.OnJoin(ctx, Join{Group: grp, PersonID: 7, Member: member}); err != nil || !kicked {
		t.Fatalf("rejoin = %v, %v; want kicked", kicked, err)
	}
	if last := kicker.kicks[len(kicker.kicks)-1]; last.prio != P0 {
		t.Errorf("rejoin kick priority %d, want P0 (§9.6)", last.prio)
	}
	if kicked, _ := b.OnJoin(ctx, Join{Group: other, PersonID: 7, Member: member}); kicked {
		t.Error("joining another group: kicked, want a local ban to stay in its group")
	}

	// Unban lifts the block and adds nobody back.
	n := len(kicker.kicks)
	if ok, err := b.Unban(ctx, grp, 7, "admin:1@lid"); err != nil || !ok {
		t.Fatalf("Unban = %v, %v", ok, err)
	}
	if kicked, _ := b.OnJoin(ctx, Join{Group: grp, PersonID: 7, Member: member}); kicked || len(kicker.kicks) != n {
		t.Error("after unban: rejoin was kicked, want the block lifted")
	}
	if ok, _ := b.Unban(ctx, grp, 7, "x"); ok {
		t.Error("second Unban reported a lifted ban")
	}
}

func TestBanWithoutRejoinBlock(t *testing.T) {
	ctx := context.Background()
	store, kicker := &memBans{}, &fakeKicker{}
	b := NewBans(store, kicker, clock.NewFake(t0))
	if _, err := b.Ban(ctx, BanRequest{Group: grp, PersonID: 7, Member: member, Reason: BanOther, DecidedBy: DecidedAdmin}); err != nil {
		t.Fatal(err)
	}
	if kicked, _ := b.OnJoin(ctx, Join{Group: grp, PersonID: 7, Member: member}); kicked {
		t.Error("rejoin_block off: rejoin kicked, want allowed")
	}
}

func TestAdminReAddLiftsBan(t *testing.T) {
	ctx := context.Background()
	store, kicker := &memBans{}, &fakeKicker{}
	b := NewBans(store, kicker, clock.NewFake(t0))
	_, _ = b.Ban(ctx, BanRequest{Group: grp, PersonID: 7, Member: member, Reason: BanSpam, DecidedBy: DecidedAuto, RejoinBlock: true})
	if kicked, err := b.OnJoin(ctx, Join{Group: grp, PersonID: 7, Member: member, AddedByAdmin: true}); err != nil || kicked {
		t.Fatalf("admin re-add = %v, %v; want no kick (the admins' own decision)", kicked, err)
	}
	if _, ok, _ := store.ActiveBan(ctx, grp, 7); ok {
		t.Error("admin re-add left the ban active")
	}
}

func TestBanRejects(t *testing.T) {
	ctx := context.Background()
	b := NewBans(&memBans{}, &fakeKicker{}, clock.NewFake(t0))
	for _, r := range []BanRequest{
		{PersonID: 7, Member: member, Reason: BanSpam, DecidedBy: DecidedAuto},
		{Group: grp, Member: member, Reason: BanSpam, DecidedBy: DecidedAuto},
		{Group: grp, PersonID: 7, Reason: BanSpam, DecidedBy: DecidedAuto},
		{Group: grp, PersonID: 7, Member: member, Reason: "grudge", DecidedBy: DecidedAuto},
		{Group: grp, PersonID: 7, Member: member, Reason: BanSpam, DecidedBy: "bot"},
	} {
		if _, err := b.Ban(ctx, r); !errors.Is(err, ErrInvalidBan) {
			t.Errorf("Ban(%+v) = %v, want ErrInvalidBan", r, err)
		}
	}
	failing := NewBans(&memBans{err: errors.New("db down")}, &fakeKicker{}, clock.NewFake(t0))
	if _, err := failing.Ban(ctx, BanRequest{Group: grp, PersonID: 7, Member: member, Reason: BanSpam, DecidedBy: DecidedAuto}); err == nil {
		t.Error("store failure: Ban = nil, want the error")
	}
}
