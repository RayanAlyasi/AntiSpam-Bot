package wpk_test

import (
	"context"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat/chattest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist/blacklisttest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

type blEnv struct {
	ctx      context.Context
	clk      *clock.Fake
	wa       *fake.Client
	list     *blacklist.MemoryList
	groups   *blacklisttest.Groups
	settings *blacklisttest.Settings
	acts     *reviewtest.Actions
	reviews  *blacklisttest.Reviews
	notifier *blacklisttest.Notifier
	budget   *blacklisttest.Budget
	linker   *blacklisttest.Linker
	audit    *reviewtest.Audit
	svc      *blacklist.Service
}

var (
	memberRow = blacklist.Member{JID: member, LID: member, Phone: memberPhone}
	adminRow  = blacklist.Member{JID: adminB, LID: adminB, Phone: "966559999999@s.whatsapp.net", Admin: true}
)

const adminBE164 = "+966559999999"

func newBlEnv(t *testing.T) *blEnv {
	t.Helper()
	e := &blEnv{
		ctx:      t.Context(),
		clk:      clock.NewFake(chattest.Start),
		wa:       fake.New(),
		list:     blacklist.NewMemoryList(),
		groups:   &blacklisttest.Groups{},
		settings: &blacklisttest.Settings{Default: blacklist.GroupSettings{UseInstance: true, Mode: review.ModeAuto, DefaultCountry: "SA"}},
		acts:     &reviewtest.Actions{},
		reviews:  &blacklisttest.Reviews{},
		notifier: &blacklisttest.Notifier{},
		budget:   &blacklisttest.Budget{},
		linker:   &blacklisttest.Linker{},
		audit:    &reviewtest.Audit{},
	}
	e.wa.SetUser(memberPhone, wa.UserInfo{LID: member})
	e.wa.SetUser(adminRow.Phone, wa.UserInfo{LID: adminB})
	e.groups.SetMembers(grp, memberRow, adminRow)
	res := blacklist.NewResolver(blacklist.ResolverDeps{Users: e.wa, Budget: e.budget, Linker: e.linker, List: e.list})
	e.svc = blacklist.NewService(blacklist.Deps{
		List: e.list, Normalize: blacklist.SimpleNormalizer{}, Resolver: res, Groups: e.groups,
		Settings: e.settings, Actions: e.acts, Reviews: e.reviews, Notifier: e.notifier, Audit: e.audit, Clock: e.clk,
	})
	return e
}

func (e *blEnv) add(t *testing.T, r blacklist.AddRequest) blacklist.AddResult {
	t.Helper()
	res, err := e.svc.Add(e.ctx, r)
	if err != nil {
		t.Fatalf("Add(%v) error = %v, want nil", r.Numbers, err)
	}
	return res
}

// assertNoSends fails on any Send: blacklist enforcement is silent (DESIGN §9.8, §9.7).
func assertNoSends(t *testing.T, acts *reviewtest.Actions) {
	t.Helper()
	if s := reviewtest.Of[review.Send](acts); len(s) != 0 {
		t.Errorf("got sends %+v, want none (blacklist enforcement is silent, DESIGN §9.8)", s)
	}
}

func auditActions(a *reviewtest.Audit, action string, actor wa.JID) int {
	n := 0
	for _, e := range a.Entries() {
		if e.Action == action && e.Actor == actor {
			n++
		}
	}
	return n
}
