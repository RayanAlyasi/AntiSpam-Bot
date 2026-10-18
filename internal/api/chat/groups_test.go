package chat_test

import (
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestNumberedGroupsAndUse: .groups numbers an admin's groups sorted by name, and .use takes a
// number from that list, an exact name (which wins, so a group named "2" stays reachable) or a
// unique part of a name. Anything else gets the numbered list again and changes nothing.
func TestNumberedGroupsAndUse(t *testing.T) {
	const named2 wa.JID = "120363000000000003@g.us"
	newRig3 := func(t *testing.T) *rig {
		r := newRigNoSite(t)
		r.groups.Protect(named2, "2")
		r.admins.Set(shop, sara, true)
		r.admins.Set(named2, sara, true)
		return r
	}

	r := newRig3(t)
	r.dm(t, sara, ".groups")
	if got, want := r.lastReply(t).Message.Text, "1. 2\n2. Riyadh Cars Market\n3. Shop"; !strings.Contains(got, want) {
		t.Fatalf(".groups = %q, want the groups numbered in name order:\n%s", got, want)
	}

	for _, tt := range []struct {
		use  string
		want wa.JID // "" means nothing is selected
	}{
		{"3", shop},
		{"٣", shop},
		{"2", named2}, // the exact name "2" wins over list number 2
		{"1", named2},
		{"riyadh", cars},
		{"RIYADH CARS MARKET", cars},
		{"0", ""},
		{"4", ""},
		{"-1", ""},
		{"s", ""}, // in two names: ambiguous
		{"", ""},
	} {
		t.Run(".use "+tt.use, func(t *testing.T) {
			r := newRig3(t)
			r.dm(t, sara, strings.TrimSpace(".use "+tt.use))
			reply := r.lastReply(t).Message.Text
			r.dm(t, sara, ".mode shadow")
			for _, g := range []wa.JID{cars, shop, named2} {
				if got, want := r.value(g, "mode") == "shadow", g == tt.want; got != want {
					t.Errorf(".use %q then .mode shadow: %s changed = %v, want %v (reply %q)", tt.use, g, got, want, reply)
				}
			}
			if tt.want == "" && !strings.Contains(reply, "1. 2\n2. Riyadh Cars Market\n3. Shop") {
				t.Errorf(".use %q = %q, want the numbered list again", tt.use, reply)
			}
		})
	}

	// .reviewgroup here lists the same numbers and takes them too.
	r = newRig3(t)
	r.in(t, admins, sara, ".reviewgroup here")
	if got := r.lastReply(t).Message.Text; !strings.Contains(got, "1. 2\n2. Riyadh Cars Market\n3. Shop") {
		t.Errorf(".reviewgroup here with three groups = %q, want the numbered list", got)
	}
	r.in(t, admins, sara, ".reviewgroup here 3")
	if got := r.value(shop, "review.review_group"); got != string(admins) {
		t.Errorf(".reviewgroup here 3: Shop's review group = %q, want %s", got, admins)
	}
}
