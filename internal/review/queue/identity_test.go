package queue_test

import (
	"context"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/queue"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

type noLIDs struct{}

func (noLIDs) LIDForPN(context.Context, wa.JID) (wa.JID, error) { return "", nil }
func (noLIDs) PNForLID(context.Context, wa.JID) (wa.JID, error) { return "", nil }

type noEvents struct{}

func (noEvents) Resolved(context.Context, identity.ResolvedEvent) error       { return nil }
func (noEvents) RecycledNumber(context.Context, identity.RecycledEvent) error { return nil }

// TestIdentityAdapters runs the identity adapters against wp-f's in-memory identity service.
func TestIdentityAdapters(t *testing.T) {
	ctx := context.Background()
	svc := identity.New(identity.NewMemoryStore(), noLIDs{}, noEvents{}, clock.NewFake(start))
	const pn wa.JID = "966551234567@s.whatsapp.net"

	persons := queue.IdentityPersons{Service: svc}
	a, err := persons.PersonID(ctx, pn)
	if err != nil || a == 0 {
		t.Fatalf("PersonID(new phone) = %d, %v; want a new person", a, err)
	}
	if err := (queue.IdentityLinker{Service: svc}).Link(ctx, pn, spam); err != nil {
		t.Fatal(err)
	}
	b, err := persons.PersonID(ctx, spam)
	if err != nil || b != a {
		t.Fatalf("PersonID(LID) = %d, %v; want the same person %d after linking (DESIGN §7.7)", b, err, a)
	}

	ids := chat.IdentityAdapter{Service: svc}
	if c, _ := ids.Canonical(ctx, pn); c != spam {
		t.Errorf("Canonical(phone) = %s, want the LID %s", c, spam)
	}
	if c, _ := ids.Canonical(ctx, "999@lid"); c != "999@lid" {
		t.Errorf("Canonical(unknown) = %s, want it unchanged", c)
	}
	if j, ok, _ := ids.ByPhone(ctx, "+966551234567"); !ok || j != spam {
		t.Errorf("ByPhone = %s, %v; want the LID", j, ok)
	}
	if _, ok, _ := ids.ByPhone(ctx, "+12025550147"); ok {
		t.Error("ByPhone(unknown) found someone, want not found")
	}

	if err := svc.AddMember(ctx, group, identity.PersonID(a)); err != nil {
		t.Fatal(err)
	}
	if err := ids.Allow(ctx, group, spam, admin); err != nil {
		t.Fatal(err)
	}
	if m, _, _ := svc.Member(ctx, group, identity.PersonID(a)); !m.Allowlisted {
		t.Error("Allow didn't allow-list the member")
	}
	if err := ids.Unallow(ctx, group, pn, admin); err != nil {
		t.Fatal(err)
	}
	if m, _, _ := svc.Member(ctx, group, identity.PersonID(a)); m.Allowlisted {
		t.Error("Unallow by phone JID didn't clear the allow-list")
	}
}
