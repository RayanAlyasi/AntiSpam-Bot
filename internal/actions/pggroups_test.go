package actions_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// postingDB answers PGGroups' two queries from maps: the groups with a settings document
// (silent or not) and the groups IsReviewGroup counts as review groups.
type postingDB struct {
	silent map[wa.JID]bool
	review map[wa.JID]bool
}

type postingRow struct {
	vals []bool
	err  error
}

func (r postingRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.vals) {
		return fmt.Errorf("scan into %d values, have %d", len(dest), len(r.vals))
	}
	for i, d := range dest {
		*d.(*bool) = r.vals[i]
	}
	return nil
}

func (d postingDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	chat := wa.JID(args[0].(string))
	switch {
	case strings.Contains(sql, "name: IsReviewGroup"):
		return postingRow{vals: []bool{d.review[chat]}}
	case strings.Contains(sql, "name: GroupPosting"):
		silent, ok := d.silent[chat]
		if !ok {
			return postingRow{err: pgx.ErrNoRows}
		}
		return postingRow{vals: []bool{silent, false}}
	}
	return postingRow{err: fmt.Errorf("unexpected query %q", sql)}
}

func (postingDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec")
}

func (postingDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected Query")
}

// TestPGGroupsReviewGroupBeforeSettings: a review group's own settings document, left from when
// it was protected, doesn't make it silent; the review group is checked first (DESIGN.md §9.5).
func TestPGGroupsReviewGroupBeforeSettings(t *testing.T) {
	g := actions.PGGroups{DB: postingDB{
		silent: map[wa.JID]bool{group(1): true, group(2): true, group(3): false},
		review: map[wa.JID]bool{group(2): true, group(4): true},
	}}
	cases := []struct {
		chat wa.JID
		want actions.Posting
		why  string
	}{
		{group(1), actions.Posting{Protected: true, Silent: true}, "a silent protected group gets no posts"},
		{group(2), actions.Posting{}, "a demoted group made a review group gets its review posts"},
		{group(3), actions.Posting{Protected: true}, "a protected group with silent off"},
		{group(4), actions.Posting{}, "a review group without a settings document"},
		{group(9), actions.Posting{Protected: true, Silent: true}, "an unknown group gets no posts"},
	}
	for _, tc := range cases {
		got, err := g.Posting(context.Background(), tc.chat)
		if err != nil || got != tc.want {
			t.Errorf("Posting(%s) = %+v, %v; want %+v (%s)", tc.chat, got, err, tc.want, tc.why)
		}
	}
}
