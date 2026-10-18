package identity

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// NewPostgresGroupStore returns the GroupStore on app.groups.
func NewPostgresGroupStore(pool *pgxpool.Pool) GroupStore { return &pgGroups{q: db.New(pool)} }

type pgGroups struct{ q *db.Queries }

func (s *pgGroups) Group(ctx context.Context, jid wa.JID) (Group, bool, error) {
	row, err := s.q.GetGroup(ctx, string(jid))
	if errors.Is(err, pgx.ErrNoRows) {
		return Group{}, false, nil
	}
	if err != nil {
		return Group{}, false, fmt.Errorf("get group %s: %w", jid, err)
	}
	return groupFromRow(db.ListGroupsRow(row)), true, nil
}

func (s *pgGroups) UpsertGroup(ctx context.Context, g Group) error {
	if !g.JID.IsGroup() {
		return fmt.Errorf("upsert group %q: not a group JID", g.JID)
	}
	err := s.q.UpsertGroup(ctx, db.UpsertGroupParams{
		Jid: string(g.JID), BotJid: string(g.Bot), Name: g.Name, Topic: g.Topic, Size: int32(g.Size),
		AddressingMode: g.AddressingMode, BotIsAdmin: g.BotIsAdmin, Announce: g.Announce, Locked: g.Locked,
		JoinApproval: g.JoinApproval, JoinedAt: optTime(g.JoinedAt), Status: string(g.Status),
		LastInfoRefresh: optTime(g.LastInfoRefresh), Kind: string(cmp.Or(g.Kind, KindGroup)),
		CommunityJid: string(g.Community), CommunityName: g.CommunityName,
	})
	if err != nil {
		return fmt.Errorf("upsert group %s: %w", g.JID, err)
	}
	return nil
}

func (s *pgGroups) Groups(ctx context.Context, statuses ...GroupStatus) ([]Group, error) {
	st := make([]string, 0, len(statuses))
	for _, x := range statuses {
		st = append(st, string(x))
	}
	rows, err := s.q.ListGroups(ctx, st)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	out := make([]Group, 0, len(rows))
	for _, r := range rows {
		out = append(out, groupFromRow(r))
	}
	return out, nil
}

func (s *pgGroups) StaleGroups(ctx context.Context, by time.Time) ([]Group, error) {
	rows, err := s.q.ListStaleGroups(ctx, tstz(by))
	if err != nil {
		return nil, fmt.Errorf("list stale groups: %w", err)
	}
	out := make([]Group, 0, len(rows))
	for _, r := range rows {
		out = append(out, groupFromRow(db.ListGroupsRow(r)))
	}
	return out, nil
}

func (s *pgGroups) CommunityGroups(ctx context.Context, community wa.JID) ([]wa.SubGroup, error) {
	rows, err := s.q.ListCommunityGroups(ctx, string(community))
	if err != nil {
		return nil, fmt.Errorf("list groups of community %s: %w", community, err)
	}
	out := make([]wa.SubGroup, 0, len(rows))
	for _, r := range rows {
		out = append(out, wa.SubGroup{JID: wa.JID(r.GroupJid), Name: r.Name, IsAnnouncement: r.Announcement})
	}
	return out, nil
}

func (s *pgGroups) SetCommunityGroups(ctx context.Context, community wa.JID, groups []wa.SubGroup) error {
	if !community.IsGroup() {
		return fmt.Errorf("set groups of community %q: not a group JID", community)
	}
	p := db.ReplaceCommunityGroupsParams{
		CommunityJid: string(community), GroupJids: []string{}, Names: []string{}, Announcements: []bool{},
	}
	seen := map[wa.JID]bool{}
	for _, g := range groups {
		if !g.JID.IsGroup() || seen[g.JID] {
			continue // one row per group: the upsert can't touch a row twice
		}
		seen[g.JID] = true
		p.GroupJids = append(p.GroupJids, string(g.JID))
		p.Names = append(p.Names, g.Name)
		p.Announcements = append(p.Announcements, g.IsAnnouncement)
	}
	if err := s.q.ReplaceCommunityGroups(ctx, p); err != nil {
		return fmt.Errorf("set groups of community %s: %w", community, err)
	}
	return nil
}

func (s *pgGroups) LinkCommunityGroup(ctx context.Context, community wa.JID, g wa.SubGroup) error {
	if !community.IsGroup() || !g.JID.IsGroup() {
		return fmt.Errorf("link %q to community %q: not a group JID", g.JID, community)
	}
	err := s.q.LinkCommunityGroup(ctx, db.LinkCommunityGroupParams{
		CommunityJid: string(community), GroupJid: string(g.JID), Name: g.Name, Announcement: g.IsAnnouncement,
	})
	if err != nil {
		return fmt.Errorf("link %s to community %s: %w", g.JID, community, err)
	}
	return nil
}

func (s *pgGroups) UnlinkCommunityGroup(ctx context.Context, community, group wa.JID) error {
	err := s.q.UnlinkCommunityGroup(ctx, db.UnlinkCommunityGroupParams{CommunityJid: string(community), GroupJid: string(group)})
	if err != nil {
		return fmt.Errorf("unlink %s from community %s: %w", group, community, err)
	}
	return nil
}

func groupFromRow(r db.ListGroupsRow) Group {
	g := Group{
		JID: wa.JID(r.Jid), Bot: wa.JID(r.BotJid), Name: r.Name, Topic: r.Topic, Size: int(r.Size),
		AddressingMode: r.AddressingMode, BotIsAdmin: r.BotIsAdmin, Announce: r.Announce, Locked: r.Locked,
		JoinApproval: r.JoinApproval, Status: GroupStatus(r.Status),
		Kind: GroupKind(r.Kind), Community: wa.JID(r.CommunityJid), CommunityName: r.CommunityName,
	}
	if r.JoinedAt.Valid {
		g.JoinedAt = r.JoinedAt.Time
	}
	if r.LastInfoRefresh.Valid {
		g.LastInfoRefresh = r.LastInfoRefresh.Time
	}
	return g
}

func optTime(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return tstz(t)
}
