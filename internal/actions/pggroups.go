package actions

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// PGGroups is the executor's Groups on Postgres. It reads posting rules from the group settings
// documents (app.group_settings, DESIGN.md §12.1) without importing the policy package, which
// enqueues through this one:
//   - a group named as some group's review group ("review.review_group") isn't protected, even
//     with a settings document left from when it was, unless the bot is active (admin) there;
//   - any other group with a settings document is protected, and silent unless "silent" is
//     false; shadow reactions are allowed when its "mode" is shadow and "shadow.reactions" is true;
//   - any other group is unknown, and treated as protected and silent: nothing is posted there.
//
// DMs are never protected.
type PGGroups struct {
	DB db.DBTX
	// OnNotAdmin records that the bot lost admin in a group (the group-state module); when nil
	// it is only logged.
	OnNotAdmin func(ctx context.Context, bot, group wa.JID) error
	Log        zerolog.Logger
}

var _ Groups = PGGroups{}

// Posting implements Groups.
func (g PGGroups) Posting(ctx context.Context, chat wa.JID) (Posting, error) {
	if !chat.IsGroup() {
		return Posting{}, nil
	}
	q := db.New(g.DB)
	// The review group comes first: a group that was once protected keeps its settings document
	// after the bot is demoted there, and an admin may then make it a review group.
	review, err := q.IsReviewGroup(ctx, string(chat))
	if err != nil {
		return Posting{}, fmt.Errorf("review groups: %w", err)
	}
	if review {
		return Posting{}, nil
	}
	row, err := q.GroupPosting(ctx, string(chat))
	switch {
	case err == nil:
		return Posting{Protected: true, Silent: row.Silent, ShadowReactions: row.ShadowReactions}, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return Posting{}, fmt.Errorf("posting rules of %s: %w", chat, err)
	}
	return Posting{Protected: true, Silent: true}, nil
}

// NotAdmin implements Groups.
func (g PGGroups) NotAdmin(ctx context.Context, bot, group wa.JID) error {
	g.Log.Warn().Str("group", string(group)).Str("bot", maskJID(bot)).Msg("the bot is no longer an admin of the group")
	if g.OnNotAdmin == nil {
		return nil
	}
	return g.OnNotAdmin(ctx, bot, group)
}
