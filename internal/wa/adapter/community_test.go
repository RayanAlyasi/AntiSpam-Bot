package adapter

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// subStub adds whatsmeow's GetSubGroups to the test stub.
type subStub struct {
	*stub
	targets []*types.GroupLinkTarget
	err     error
}

func (s subStub) GetSubGroups(context.Context, types.JID) ([]*types.GroupLinkTarget, error) {
	return s.targets, s.err
}

func TestSubGroups(t *testing.T) {
	const community wa.JID = "120363100@g.us"
	cars := &types.GroupLinkTarget{JID: types.NewJID("120363101", types.GroupServer), GroupName: types.GroupName{Name: "Cars"}}
	news := &types.GroupLinkTarget{
		JID: types.NewJID("120363102", types.GroupServer), GroupName: types.GroupName{Name: "News"},
		GroupIsDefaultSub: types.GroupIsDefaultSub{IsDefaultSubGroup: true},
	}
	tests := []struct {
		name string
		cli  api
		want []wa.SubGroup
		err  error
	}{
		{"listed", subStub{stub: &stub{}, targets: []*types.GroupLinkTarget{cars, nil, news}}, []wa.SubGroup{
			{JID: "120363101@g.us", Name: "Cars"}, {JID: "120363102@g.us", Name: "News", IsAnnouncement: true},
		}, nil},
		{"rate limited", subStub{stub: &stub{}, err: whatsmeow.ErrIQRateOverLimit}, nil, wa.ErrRateLimited},
		{"client without the call", &stub{}, nil, errors.ErrUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{cli: tt.cli, clock: clock.NewFake(time.Unix(1000, 0))}
			got, err := c.SubGroups(t.Context(), community)
			if !errors.Is(err, tt.err) || !slices.Equal(got, tt.want) {
				t.Errorf("SubGroups = %v, %v; want %v, %v", got, err, tt.want, tt.err)
			}
		})
	}
}
