package fake

import (
	"errors"
	"slices"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestSubGroups: a community lists the groups linked to it that the bot is in and those set with
// SetSubGroup; an unknown community is wa.ErrNotFound.
func TestSubGroups(t *testing.T) {
	const (
		community wa.JID = "120363000000000100@g.us"
		cars      wa.JID = "120363000000000101@g.us"
		phones    wa.JID = "120363000000000102@g.us"
	)
	c := New()
	c.SetGroup(wa.GroupInfo{JID: community, Name: "Riyadh Market", IsCommunity: true})
	c.SetGroup(wa.GroupInfo{JID: cars, Name: "Cars", LinkedParent: community})
	c.SetGroup(wa.GroupInfo{JID: group, Name: "Elsewhere"})
	c.SetSubGroup(community, wa.SubGroup{JID: phones, Name: "Phones"})

	tests := []struct {
		community wa.JID
		want      []wa.SubGroup
		err       error
	}{
		{community, []wa.SubGroup{{JID: cars, Name: "Cars"}, {JID: phones, Name: "Phones"}}, nil},
		{"120363000000000199@g.us", nil, wa.ErrNotFound},
	}
	for _, tt := range tests {
		got, err := c.SubGroups(t.Context(), tt.community)
		if !errors.Is(err, tt.err) || !slices.Equal(got, tt.want) {
			t.Errorf("SubGroups(%s) = %v, %v; want %v, %v", tt.community, got, err, tt.want, tt.err)
		}
	}
	if n := len(c.CallsTo(MethodSubGroups)); n != 2 {
		t.Errorf("recorded %d SubGroups calls, want 2", n)
	}
	c.FailNext(MethodSubGroups, wa.ErrTimeout)
	if _, err := c.SubGroups(t.Context(), community); !errors.Is(err, wa.ErrTimeout) {
		t.Errorf("SubGroups with an injected error = %v, want wa.ErrTimeout", err)
	}
}
