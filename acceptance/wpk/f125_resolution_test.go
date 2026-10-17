package wpk_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist/blacklisttest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// TestF125_ResolvesInBatchesWithinReadBudget: lookups are batched (≤ Batch numbers per
// GetUserInfo) and each call waits on the read budget once (DESIGN §9.8 step 1, §10.3).
func TestF125_ResolvesInBatchesWithinReadBudget(t *testing.T) {
	cases := []struct {
		name      string
		batch     int
		numbers   int
		wantCalls int
		wantMax   int
	}{
		{"default batch of 50", 0, 120, 3, blacklist.DefaultBatch},
		{"batch of 10", 10, 25, 3, 10},
		{"single call", 0, 7, 1, blacklist.DefaultBatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newBlEnv(t)
			var nums []string
			for i := range tc.numbers {
				n := fmt.Sprintf("+9665500%05d", i)
				nums = append(nums, n)
				if _, _, err := e.list.Add(e.ctx, blacklist.Entry{Scope: blacklist.ScopeGroup, Group: grp, E164: n, AddedBy: adminA}); err != nil {
					t.Fatalf("list.Add: %v", err)
				}
			}
			client := fake.New()
			budget := &blacklisttest.Budget{}
			r := blacklist.NewResolver(blacklist.ResolverDeps{Users: client, Budget: budget, Linker: &blacklisttest.Linker{}, List: e.list, Batch: tc.batch})
			res, err := r.Resolve(e.ctx, nums)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if len(res) != tc.numbers {
				t.Errorf("got %d resolutions, want %d (one per number)", len(res), tc.numbers)
			}
			calls := client.CallsTo(fake.MethodUserInfo)
			if len(calls) != tc.wantCalls {
				t.Errorf("got %d UserInfo calls, want %d (lookups are batched, DESIGN §9.8)", len(calls), tc.wantCalls)
			}
			for i, c := range calls {
				if jids, _ := c.Args[0].([]wa.JID); len(jids) > tc.wantMax {
					t.Errorf("call %d looked up %d numbers, want at most %d", i, len(jids), tc.wantMax)
				}
			}
			if budget.Waits() != len(calls) {
				t.Errorf("got %d read-budget waits for %d calls, want one per call (reads are paced, DESIGN §10.3)", budget.Waits(), len(calls))
			}
		})
	}
}

// TestF125_LinksIdentitiesAndKeepsUnknownNumbers: a found LID is linked to the phone identity and
// recorded on the entry; a number not on WhatsApp stays listed as a content signal (DESIGN §9.8).
func TestF125_LinksIdentitiesAndKeepsUnknownNumbers(t *testing.T) {
	e := newBlEnv(t)
	const ghost = "+966551230000"
	for _, n := range []string{memberE164, ghost} {
		if _, _, err := e.list.Add(e.ctx, blacklist.Entry{Scope: blacklist.ScopeGroup, Group: grp2, E164: n, AddedBy: adminA}); err != nil {
			t.Fatalf("list.Add: %v", err)
		}
	}
	r := blacklist.NewResolver(blacklist.ResolverDeps{Users: e.wa, Budget: e.budget, Linker: e.linker, List: e.list})
	res, err := r.Resolve(e.ctx, []string{memberE164, ghost})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	byNum := map[string]blacklist.Resolution{}
	for _, x := range res {
		byNum[x.E164] = x
	}
	if got := byNum[memberE164]; !got.OnWhatsApp || got.LID != member || got.Phone != memberPhone {
		t.Errorf("got %+v, want {OnWhatsApp:true LID:%s Phone:%s} (DESIGN §9.8 resolve the identity)", got, member, memberPhone)
	}
	if got := byNum[ghost]; got.OnWhatsApp || got.LID != "" {
		t.Errorf("got %+v for a number not on WhatsApp, want OnWhatsApp false and no LID", got)
	}
	links := e.linker.All()
	if len(links) != 1 || links[0] != (blacklisttest.Link{Phone: memberPhone, LID: member}) {
		t.Errorf("got links %+v, want exactly {%s %s} (both identities link to the person, DESIGN §7.7)", links, memberPhone, member)
	}
	if got, _ := e.list.ByPerson(e.ctx, member); len(got) != 1 || got[0].E164 != memberE164 {
		t.Errorf("ByPerson(%s) = %+v, want the resolved entry (the LID is recorded on the entry)", member, got)
	}
	ghosts, _ := e.list.ByNumber(e.ctx, ghost)
	if len(ghosts) != 1 || ghosts[0].OnWhatsApp == nil || *ghosts[0].OnWhatsApp {
		t.Errorf("got %+v for the unknown number, want it kept with OnWhatsApp=false (still a content signal, DESIGN §9.8)", ghosts)
	}
	if got := (blacklist.Entry{E164: memberE164}).PhoneJID(); got != memberPhone {
		t.Errorf("PhoneJID() = %s, want %s", got, memberPhone)
	}
}

// TestF125_NoLookupWithoutBudget: when the read budget refuses, no GetUserInfo call is made
// (DESIGN §10.3: reads stay inside their budget).
func TestF125_NoLookupWithoutBudget(t *testing.T) {
	e := newBlEnv(t)
	client := fake.New()
	refused := errors.New("read budget exhausted")
	r := blacklist.NewResolver(blacklist.ResolverDeps{Users: client, Budget: &blacklisttest.Budget{Err: refused}, Linker: e.linker, List: e.list})
	if _, err := r.Resolve(e.ctx, []string{memberE164}); !errors.Is(err, refused) {
		t.Errorf("Resolve() error = %v, want the budget's error wrapped", err)
	}
	if n := len(client.CallsTo(fake.MethodUserInfo)); n != 0 {
		t.Errorf("got %d UserInfo calls, want 0 (never read past the budget, DESIGN §10.3)", n)
	}
}
