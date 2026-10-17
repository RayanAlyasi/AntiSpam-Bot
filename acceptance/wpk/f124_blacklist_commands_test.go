package wpk_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
)

// TestF124_AddNormalizesAndReportsInvalid: numbers in common formats are stored as E.164, invalid
// ones are reported back, duplicates are reported, and each add is audited (DESIGN §9.8).
func TestF124_AddNormalizesAndReportsInvalid(t *testing.T) {
	e := newBlEnv(t)
	res := e.add(t, blacklist.AddRequest{Group: grp, Numbers: []string{"+966 55 123 4567", "0551234568", "wa.me/966551234569", "12ab"}, Reason: "crypto spam", By: adminA})

	var got []string
	for _, a := range res.Added {
		got = append(got, a.E164)
	}
	slices.Sort(got)
	want := []string{"+966551234567", "+966551234568", "+966551234569"}
	if !slices.Equal(got, want) {
		t.Errorf("got added %v, want %v (numbers are normalized to E.164, DESIGN §9.8)", got, want)
	}
	if !slices.Equal(res.Invalid, []string{"12ab"}) {
		t.Errorf("got invalid %v, want [12ab] (invalid numbers are reported back, not dropped, DESIGN §9.8)", res.Invalid)
	}
	entries, err := e.svc.ForGroup(e.ctx, grp)
	if err != nil || len(entries) != 3 {
		t.Fatalf("ForGroup() = %d entries, %v; want 3", len(entries), err)
	}
	for _, en := range entries {
		if en.Scope != blacklist.ScopeGroup || en.Group != grp || en.Reason != "crypto spam" || en.AddedBy != adminA {
			t.Errorf("got entry %+v, want scope group, group %s, reason and adder recorded (DESIGN §14 number_blacklist)", en, grp)
		}
	}
	if auditActions(e.audit, "blacklist.add", adminA) == 0 {
		t.Errorf("no blacklist.add audit entry by %s, want one (every add is logged, DESIGN §9.8)", adminA)
	}

	again := e.add(t, blacklist.AddRequest{Group: grp, Numbers: []string{"00966551234567"}, By: adminA})
	if len(again.Added) != 0 || !slices.Equal(again.Already, []string{memberE164}) {
		t.Errorf("got added %v, already %v; want none added, already [%s]", again.Added, again.Already, memberE164)
	}
}

// TestF124_RemoveAndAudit: remove takes numbers off the list, reports unknown ones and is
// audited (DESIGN §9.8 undo).
func TestF124_RemoveAndAudit(t *testing.T) {
	e := newBlEnv(t)
	e.add(t, blacklist.AddRequest{Group: grp, Numbers: []string{"+966551234568"}, By: adminA})
	res, err := e.svc.Remove(e.ctx, blacklist.RemoveRequest{Group: grp, Numbers: []string{"0551234568", "+966551230000", "x"}, By: adminA})
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if !slices.Equal(res.Removed, []string{"+966551234568"}) || !slices.Equal(res.NotFound, []string{"+966551230000"}) || !slices.Equal(res.Invalid, []string{"x"}) {
		t.Errorf("got %+v, want removed [+966551234568], not found [+966551230000], invalid [x]", res)
	}
	if entries, _ := e.svc.ForGroup(e.ctx, grp); len(entries) != 0 {
		t.Errorf("got %d entries after remove, want 0", len(entries))
	}
	if auditActions(e.audit, "blacklist.remove", adminA) == 0 {
		t.Errorf("no blacklist.remove audit entry, want one (every remove is logged, DESIGN §9.8)")
	}
}

// TestF124_InstanceScopeIsOperatorOnly: only the operator adds or removes instance-wide entries;
// groups see them unless blacklist.use_instance is false (DESIGN §9.8 Scope).
func TestF124_InstanceScopeIsOperatorOnly(t *testing.T) {
	e := newBlEnv(t)
	if _, err := e.svc.Add(e.ctx, blacklist.AddRequest{Group: grp, Numbers: []string{"+966551234568"}, By: adminA, Instance: true}); !errors.Is(err, blacklist.ErrNotOperator) {
		t.Errorf("non-operator --instance add error = %v, want ErrNotOperator (operator only, DESIGN §9.8)", err)
	}
	if c, _ := e.svc.Check(e.ctx, grp, "+966551234568"); c.Instance != nil || c.Group != nil {
		t.Errorf("got check %+v after a refused add, want nothing listed", c)
	}

	e.add(t, blacklist.AddRequest{Numbers: []string{"+966551234568"}, By: operator, Operator: true, Instance: true})
	c, err := e.svc.Check(e.ctx, grp, "0551234568")
	if err != nil || c.Instance == nil || c.Instance.Scope != blacklist.ScopeInstance || c.Group != nil {
		t.Errorf("Check() = %+v, %v; want an instance entry only (DESIGN §9.8)", c, err)
	}
	e.settings.Set(grp2, blacklist.GroupSettings{UseInstance: false, Mode: review.ModeAuto, DefaultCountry: "SA"})
	if c, _ := e.svc.Check(e.ctx, grp2, "+966551234568"); c.Instance != nil {
		t.Errorf("group with use_instance=false sees the instance entry, want it ignored (DESIGN §9.8)")
	}
	if _, err := e.svc.Remove(e.ctx, blacklist.RemoveRequest{Group: grp, Numbers: []string{"+966551234568"}, By: adminA, Instance: true}); !errors.Is(err, blacklist.ErrNotOperator) {
		t.Errorf("non-operator --instance remove error = %v, want ErrNotOperator", err)
	}
}

// TestF124_CheckReportsMembership: check says whether the number is listed here and whether its
// owner is a member (DESIGN §9.8).
func TestF124_CheckReportsMembership(t *testing.T) {
	e := newBlEnv(t)
	if c, err := e.svc.Check(e.ctx, grp, memberE164); err != nil || c.Group != nil || !c.Member || c.E164 != memberE164 {
		t.Errorf("Check() before add = %+v, %v; want not listed, member, E164 %s", c, err, memberE164)
	}
	if c, _ := e.svc.Check(e.ctx, grp, "+966551230000"); c.Member {
		t.Errorf("Check() of a stranger says member, want not a member")
	}
}

// TestF124_BlacklistCommands: the chat commands add (reporting invalid input), list masked
// numbers and refuse --instance from non-operators; replies never go into the group
// (DESIGN §9.8, §13.7).
func TestF124_BlacklistCommands(t *testing.T) {
	e := newChatEnv(t)
	e.openThread(t, adminA)
	e.handle(t, e.inGroup(adminA, ".blacklist add +966551234568 +12 crypto scam"))

	entries, _ := e.bl.ForGroup(e.ctx, grp)
	if len(entries) != 1 || entries[0].E164 != "+966551234568" || entries[0].AddedBy != adminA {
		t.Fatalf("got entries %+v, want one for +966551234568 added by %s (DESIGN §9.8)", entries, adminA)
	}
	if !strings.Contains(entries[0].Reason, "crypto scam") {
		t.Errorf("got reason %q, want it to keep %q (DESIGN §9.8 [reason])", entries[0].Reason, "crypto scam")
	}
	var reply string
	for _, s := range sendsTo(e.acts, adminA) {
		reply += s.Message.Text + "\n"
	}
	if !strings.Contains(reply, "+12") {
		t.Errorf("reply %q doesn't mention the invalid input +12, want it reported back (DESIGN §9.8)", reply)
	}

	e.acts.Reset()
	e.handle(t, e.inGroup(adminA, ".blacklist list"))
	reply = ""
	for _, s := range sendsTo(e.acts, adminA) {
		reply += s.Message.Text + "\n"
	}
	if !strings.Contains(reply, review.MaskNumber("+966551234568")) {
		t.Errorf("list reply %q lacks the masked number %q, want entries shown masked (DESIGN §9.8)", reply, review.MaskNumber("+966551234568"))
	}
	if strings.Contains(reply, "551234568") || strings.Contains(reply, "1234568") {
		t.Errorf("list reply %q shows the number, want all but the last two digits masked (DESIGN §9.8)", reply)
	}

	e.handle(t, e.inGroup(adminA, ".blacklist add --instance +966551234569"))
	if c, _ := e.bl.Check(e.ctx, grp, "+966551234569"); c.Instance != nil || c.Group != nil {
		t.Errorf("non-operator --instance add stored %+v, want refused (operator only, DESIGN §9.8)", c)
	}
	e.handle(t, e.inGroup(operator, ".blacklist add --instance +966551234569"))
	if c, _ := e.bl.Check(e.ctx, grp, "+966551234569"); c.Instance == nil {
		t.Errorf("operator --instance add not stored, want an instance entry (DESIGN §9.8)")
	}

	e.handle(t, e.inGroup(member2, ".blacklist add +966551234570"))
	if c, _ := e.bl.Check(e.ctx, grp, "+966551234570"); c.Group != nil {
		t.Errorf("non-admin .blacklist add stored an entry, want ignored (DESIGN §13.7)")
	}
	assertSilent(t, e.acts, grp)
}
