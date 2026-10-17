package identity_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Test identifiers. Same user digits on different servers are different identities.
const (
	lidA wa.JID = "100000000000001@lid"
	lidB wa.JID = "100000000000002@lid"
	lidC wa.JID = "100000000000003@lid"
	pnSA wa.JID = "966501234567@s.whatsapp.net" // Saudi Arabia, +966
	pnUK wa.JID = "447700900123@s.whatsapp.net" // United Kingdom, +44
	pnUS wa.JID = "14155550123@s.whatsapp.net"  // United States, +1
	pnEG wa.JID = "201001234567@s.whatsapp.net" // Egypt, +20

	groupOne   wa.JID = "120363000000000001@g.us"
	groupTwo   wa.JID = "120363000000000002@g.us"
	groupThree wa.JID = "120363000000000003@g.us"
)

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// fakeLIDs stands in for whatsmeow's LID store (PNForLID, local) and the batched usync
// lookup (LIDForPN). It counts calls so tests can prove no extra lookups are made.
type fakeLIDs struct {
	mu            sync.Mutex
	pnToLID       map[wa.JID]wa.JID
	lidToPN       map[wa.JID]wa.JID
	lidForPNErr   error
	lidForPNCalls int
	pnForLIDCalls int
}

func newFakeLIDs() *fakeLIDs {
	return &fakeLIDs{pnToLID: map[wa.JID]wa.JID{}, lidToPN: map[wa.JID]wa.JID{}}
}

func (f *fakeLIDs) LIDForPN(_ context.Context, pn wa.JID) (wa.JID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lidForPNCalls++
	if f.lidForPNErr != nil {
		return "", f.lidForPNErr
	}
	return f.pnToLID[pn], nil
}

func (f *fakeLIDs) PNForLID(_ context.Context, lid wa.JID) (wa.JID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pnForLIDCalls++
	return f.lidToPN[lid], nil
}

func (f *fakeLIDs) set(lid, pn wa.JID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pnToLID[pn] = lid
	f.lidToPN[lid] = pn
}

func (f *fakeLIDs) calls() (lidForPN, pnForLID int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lidForPNCalls, f.pnForLIDCalls
}

// recorder collects identity events.
type recorder struct {
	mu       sync.Mutex
	resolved []identity.ResolvedEvent
	recycled []identity.RecycledEvent
}

func (r *recorder) Resolved(_ context.Context, ev identity.ResolvedEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolved = append(r.resolved, ev)
	return nil
}

func (r *recorder) RecycledNumber(_ context.Context, ev identity.RecycledEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recycled = append(r.recycled, ev)
	return nil
}

func (r *recorder) snapshot() ([]identity.ResolvedEvent, []identity.RecycledEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.resolved), slices.Clone(r.recycled)
}

type harness struct {
	svc    *identity.Service
	lids   *fakeLIDs
	events *recorder
	clk    *clock.Fake
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{lids: newFakeLIDs(), events: &recorder{}, clk: clock.NewFake(t0)}
	h.svc = identity.New(identity.NewMemoryStore(), h.lids, h.events, h.clk)
	return h
}

func (h *harness) ensure(t *testing.T, jid wa.JID, src identity.Source) identity.Person {
	t.Helper()
	p, err := h.svc.Ensure(context.Background(), jid, src)
	if err != nil {
		t.Fatalf("Ensure(%s, %s): unexpected error %v (a person JID must always get a person record, DESIGN §7.7)", jid, src, err)
	}
	if p.ID == 0 {
		t.Fatalf("Ensure(%s): got PersonID 0, want a non-zero internal ID (every person gets a persons.id, DESIGN §7.7)", jid)
	}
	return p
}

func (h *harness) learn(t *testing.T, lid, pn wa.JID, src identity.Source) identity.Person {
	t.Helper()
	p, err := h.svc.Learn(context.Background(), lid, pn, src)
	if err != nil {
		t.Fatalf("Learn(%s, %s, %s): unexpected error %v (a LID/PN pair from a free source must be recorded, DESIGN §7.7)", lid, pn, src, err)
	}
	return p
}

func (h *harness) match(t *testing.T, jid wa.JID) (identity.Person, bool) {
	t.Helper()
	p, ok, err := h.svc.MatchPerson(context.Background(), jid)
	if err != nil {
		t.Fatalf("MatchPerson(%s): unexpected error %v (lookup of a JID must not fail, DESIGN §7.7)", jid, err)
	}
	return p, ok
}

func (h *harness) mustMatch(t *testing.T, jid wa.JID) identity.Person {
	t.Helper()
	p, ok := h.match(t, jid)
	if !ok {
		t.Fatalf("MatchPerson(%s): not found, want a person (MatchPerson finds a person by LID or PN alone, DESIGN §7.7)", jid)
	}
	return p
}

func (h *harness) identities(t *testing.T, id identity.PersonID) []identity.Identity {
	t.Helper()
	ids, err := h.svc.Identities(context.Background(), id)
	if err != nil {
		t.Fatalf("Identities(%d): unexpected error %v", id, err)
	}
	return ids
}

func (h *harness) addMember(t *testing.T, group wa.JID, id identity.PersonID) {
	t.Helper()
	if err := h.svc.AddMember(context.Background(), group, id); err != nil {
		t.Fatalf("AddMember(%s, %d): unexpected error %v", group, id, err)
	}
}

func (h *harness) groupsOf(t *testing.T, id identity.PersonID) []wa.JID {
	t.Helper()
	gs, err := h.svc.GroupsOf(context.Background(), id)
	if err != nil {
		t.Fatalf("GroupsOf(%d): unexpected error %v", id, err)
	}
	slices.Sort(gs)
	return gs
}

// findIdentity returns the identity row with the given kind and value, preferring an open one.
func findIdentity(ids []identity.Identity, kind identity.Kind, value wa.JID) (identity.Identity, bool) {
	var found identity.Identity
	ok := false
	for _, id := range ids {
		if id.Kind != kind || id.Value != value {
			continue
		}
		if id.ValidTo == nil {
			return id, true
		}
		found, ok = id, true
	}
	return found, ok
}

func openCount(ids []identity.Identity) int {
	n := 0
	for _, id := range ids {
		if id.ValidTo == nil {
			n++
		}
	}
	return n
}

func sorted(gs ...wa.JID) []wa.JID {
	out := slices.Clone(gs)
	slices.Sort(out)
	return out
}
