package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// SettingsChannel is the NOTIFY channel a settings save signals, with the group JID as payload.
const SettingsChannel = "group_settings"

// AuditSettingsUpdate is the audit_log action of a settings change.
const AuditSettingsUpdate = "settings.update"

// PolicyStore implements the policy package's storage ports on Postgres: group settings with
// history, audit and NOTIFY (DESIGN.md §12.1), detections (§7.3, §8.4) and local bans (§9.6).
type PolicyStore struct {
	pool *pgxpool.Pool
}

// NewPolicyStore returns a PolicyStore over pool.
func NewPolicyStore(pool *pgxpool.Pool) *PolicyStore { return &PolicyStore{pool: pool} }

var (
	_ policy.SettingsRepo   = (*PolicyStore)(nil)
	_ policy.BanStore       = (*PolicyStore)(nil)
	_ policy.DetectionStore = (*PolicyStore)(nil)
)

// LoadSettings returns the group's current settings document.
func (s *PolicyStore) LoadSettings(ctx context.Context, group wa.JID) (policy.StoredSettings, bool, error) {
	row, err := db.New(s.pool).GetGroupSettings(ctx, string(group))
	if errors.Is(err, pgx.ErrNoRows) {
		return policy.StoredSettings{}, false, nil
	}
	if err != nil {
		return policy.StoredSettings{}, false, fmt.Errorf("get group_settings %s: %w", group, err)
	}
	return policy.StoredSettings{Doc: row.Doc, Version: int(row.Version), UpdatedBy: row.UpdatedBy, UpdatedAt: row.UpdatedAt}, true, nil
}

// SaveSettings stores the next version of a group's document if it is still at c.PrevVersion.
// The new version, its history row, the audit entry and the NOTIFY commit together.
func (s *PolicyStore) SaveSettings(ctx context.Context, c policy.SettingsChange) (int, error) {
	if c.Group == "" || c.Actor == "" {
		return 0, fmt.Errorf("save settings: %w: group and actor are required", policy.ErrInvalidSettings)
	}
	var version int
	err := InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		var row db.AppGroupSetting
		var err error
		if c.PrevVersion == 0 {
			row, err = q.InsertGroupSettings(ctx, db.InsertGroupSettingsParams{GroupJid: string(c.Group), Doc: c.Doc, UpdatedBy: c.Actor})
			if isUniqueViolation(err) {
				return policy.ErrSettingsConflict
			}
		} else {
			row, err = q.UpdateGroupSettings(ctx, db.UpdateGroupSettingsParams{
				GroupJid: string(c.Group), Doc: c.Doc, UpdatedBy: c.Actor, PrevVersion: int32(c.PrevVersion),
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return policy.ErrSettingsConflict
			}
		}
		if err != nil {
			return fmt.Errorf("write group_settings %s: %w", c.Group, err)
		}
		if err := q.InsertGroupSettingsHistory(ctx, db.InsertGroupSettingsHistoryParams(row)); err != nil {
			return fmt.Errorf("write group_settings_history %s v%d: %w", c.Group, row.Version, err)
		}
		if _, err := WriteAudit(ctx, tx, AuditEntry{
			Actor: c.Actor, GroupJID: string(c.Group), Action: AuditSettingsUpdate, Diff: c.AuditDiff(int(row.Version)),
		}); err != nil {
			return err
		}
		if err := q.NotifyGroupSettings(ctx, string(c.Group)); err != nil {
			return fmt.Errorf("notify %s: %w", SettingsChannel, err)
		}
		version = int(row.Version)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return version, nil
}

// SettingsHistory returns a group's stored versions, newest first.
func (s *PolicyStore) SettingsHistory(ctx context.Context, group wa.JID, limit int32) ([]policy.StoredSettings, error) {
	if limit <= 0 {
		return []policy.StoredSettings{}, nil
	}
	rows, err := db.New(s.pool).ListGroupSettingsHistory(ctx, db.ListGroupSettingsHistoryParams{GroupJid: string(group), RowLimit: limit})
	if err != nil {
		return nil, fmt.Errorf("list group_settings_history %s: %w", group, err)
	}
	out := make([]policy.StoredSettings, len(rows))
	for i, r := range rows {
		out[i] = policy.StoredSettings{Doc: r.Doc, Version: int(r.Version), UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt}
	}
	return out, nil
}

// ListenSettings calls invalidate with the group JID of every settings change any process
// saves, until ctx ends (DESIGN.md §11: caches refresh via NOTIFY). It holds one pooled
// connection. It returns ctx.Err() when ctx ends, or the connection error otherwise; the
// caller restarts it and then calls SettingsService.InvalidateAll, since changes may have been
// missed meanwhile.
func (s *PolicyStore) ListenSettings(ctx context.Context, invalidate func(group wa.JID)) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("listen %s: acquire: %w", SettingsChannel, err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+SettingsChannel); err != nil {
		return fmt.Errorf("listen %s: %w", SettingsChannel, err)
	}
	defer func() {
		// The connection goes back to the pool; it must not keep receiving notifications.
		_, _ = conn.Exec(context.WithoutCancel(ctx), "UNLISTEN "+SettingsChannel)
	}()
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("listen %s: %w", SettingsChannel, err)
		}
		if n.Channel == SettingsChannel {
			invalidate(wa.JID(n.Payload))
		}
	}
}

// InsertDetection stores a detection row. Signals and violations go into the signals column;
// the action plan holds the would-be (shadow) or enqueued (enforce) actions.
func (s *PolicyStore) InsertDetection(ctx context.Context, d policy.DetectionRecord) (policy.DetectionRecord, error) {
	if d.Group == "" || d.MsgID == "" {
		return policy.DetectionRecord{}, errors.New("insert detection: group and message ID are required")
	}
	if math.IsNaN(d.Score) || d.Score < 0 || d.Score > 1 {
		return policy.DetectionRecord{}, fmt.Errorf("insert detection: score %v outside 0..1", d.Score)
	}
	// Violations are stored by rule only: their details can quote the message (a file name, a
	// link host), and detections hold no message text (DESIGN.md §14, §17).
	rules := make([]string, 0, len(d.Violations))
	for _, v := range d.Violations {
		rules = append(rules, v.Rule)
	}
	signals, err := json.Marshal(struct {
		Signals    []policy.Signal `json:"signals"`
		Violations []string        `json:"violations"`
		Reason     string          `json:"reason"`
	}{orEmpty(d.Signals), rules, d.Reason})
	if err != nil {
		return policy.DetectionRecord{}, fmt.Errorf("encode detection signals: %w", err)
	}
	plan, err := json.Marshal(planJSON(d.ActionPlan))
	if err != nil {
		return policy.DetectionRecord{}, fmt.Errorf("encode detection plan: %w", err)
	}
	row, err := db.New(s.pool).InsertDetection(ctx, db.InsertDetectionParams{
		GroupJid:         string(d.Group),
		MsgID:            string(d.MsgID),
		PersonID:         optInt8(d.PersonID),
		Score:            d.Score,
		TierReached:      d.TierReached,
		Signals:          signals,
		Verdict:          string(d.Verdict),
		ConfidenceSource: pgtype.Text{String: string(d.Source), Valid: d.Source != policy.SourceNone},
		Route:            string(d.Route),
		RuleVersion:      d.RuleVersion,
		ActionPlan:       plan,
		Mode:             string(d.Mode),
		EvidenceRef:      pgtype.Text{String: d.EvidenceRef, Valid: d.EvidenceRef != ""},
	})
	if err != nil {
		return policy.DetectionRecord{}, fmt.Errorf("insert detection %s/%s: %w", d.Group, d.MsgID, err)
	}
	d.ID, d.CreatedAt = row.ID, row.CreatedAt
	return d, nil
}

// ListDetections returns a group's detections, newest first, as stored rows.
func (s *PolicyStore) ListDetections(ctx context.Context, group wa.JID, beforeID int64, limit int32) ([]db.AppDetection, error) {
	if limit <= 0 {
		return []db.AppDetection{}, nil
	}
	rows, err := db.New(s.pool).ListDetectionsByGroup(ctx, db.ListDetectionsByGroupParams{GroupJid: string(group), BeforeID: beforeID, RowLimit: limit})
	if err != nil {
		return nil, fmt.Errorf("list detections %s: %w", group, err)
	}
	return rows, nil
}

// CreateBan records an active ban, or returns the person's existing active ban in the group.
func (s *PolicyStore) CreateBan(ctx context.Context, b policy.GroupBan) (policy.GroupBan, bool, error) {
	q := db.New(s.pool)
	row, err := q.InsertGroupBan(ctx, db.InsertGroupBanParams{
		GroupJid: string(b.Group), PersonID: b.PersonID, Reason: string(b.Reason), DecidedBy: string(b.DecidedBy),
		DeciderPerson: optInt8(b.DeciderPerson), DetectionID: optInt8(b.DetectionID), ReviewID: optInt8(b.ReviewID),
		RejoinBlock: b.RejoinBlock, CreatedAt: pgtype.Timestamptz{Time: b.CreatedAt, Valid: !b.CreatedAt.IsZero()},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, ok, err := s.ActiveBan(ctx, b.Group, b.PersonID)
		if err != nil {
			return policy.GroupBan{}, false, err
		}
		if !ok {
			return policy.GroupBan{}, false, fmt.Errorf("record ban in %s: an active ban conflicted but is gone", b.Group)
		}
		return existing, false, nil
	}
	if err != nil {
		return policy.GroupBan{}, false, fmt.Errorf("insert group_bans %s: %w", b.Group, err)
	}
	return banFromRow(row), true, nil
}

// ActiveBan returns the person's active ban in the group.
func (s *PolicyStore) ActiveBan(ctx context.Context, group wa.JID, personID int64) (policy.GroupBan, bool, error) {
	row, err := db.New(s.pool).GetActiveGroupBan(ctx, db.GetActiveGroupBanParams{GroupJid: string(group), PersonID: personID})
	if errors.Is(err, pgx.ErrNoRows) {
		return policy.GroupBan{}, false, nil
	}
	if err != nil {
		return policy.GroupBan{}, false, fmt.Errorf("get active ban %s: %w", group, err)
	}
	return banFromRow(row), true, nil
}

// LiftBan deactivates the person's active ban in the group.
func (s *PolicyStore) LiftBan(ctx context.Context, group wa.JID, personID int64, liftedBy string, at time.Time) (bool, error) {
	n, err := db.New(s.pool).LiftGroupBan(ctx, db.LiftGroupBanParams{
		GroupJid: string(group), PersonID: personID,
		LiftedBy: pgtype.Text{String: liftedBy, Valid: true}, LiftedAt: pgtype.Timestamptz{Time: at, Valid: true},
	})
	if err != nil {
		return false, fmt.Errorf("lift ban %s: %w", group, err)
	}
	return n > 0, nil
}

// BanTimesSince returns when the group's bans since since were made, to seed the hourly ban cap
// (policy.Rails.NoteBans) after a restart.
func (s *PolicyStore) BanTimesSince(ctx context.Context, group wa.JID, since time.Time) ([]time.Time, error) {
	ts, err := db.New(s.pool).ListGroupBanTimesSince(ctx, db.ListGroupBanTimesSinceParams{GroupJid: string(group), Since: since})
	if err != nil {
		return nil, fmt.Errorf("list ban times %s: %w", group, err)
	}
	return ts, nil
}

// plannedAction is how an action is stored in detections.action_plan.
type plannedAction struct {
	Kind     string         `json:"kind"`
	Priority int            `json:"priority"`
	Review   *plannedReview `json:"review,omitempty"`
}

type plannedReview struct {
	Kind       string `json:"kind"`
	ProposeBan bool   `json:"propose_ban"`
	Timeout    string `json:"timeout"`
	OnTimeout  string `json:"on_timeout"`
	Batch      string `json:"batch,omitempty"`
}

func planJSON(actions []policy.Action) []plannedAction {
	out := make([]plannedAction, 0, len(actions))
	for _, a := range actions {
		p := plannedAction{Kind: string(a.Kind), Priority: a.Priority}
		if r := a.Review; r != nil {
			p.Review = &plannedReview{Kind: string(r.Kind), ProposeBan: r.ProposeBan, Timeout: r.Timeout.String(),
				OnTimeout: string(r.OnTimeout), Batch: r.Batch}
		}
		out = append(out, p)
	}
	return out
}

func banFromRow(r db.AppGroupBan) policy.GroupBan {
	b := policy.GroupBan{
		ID: r.ID, Group: wa.JID(r.GroupJid), PersonID: r.PersonID, Reason: policy.BanReason(r.Reason),
		DecidedBy: policy.BanDecider(r.DecidedBy), DeciderPerson: ptr8(r.DeciderPerson), DetectionID: ptr8(r.DetectionID),
		ReviewID: ptr8(r.ReviewID), RejoinBlock: r.RejoinBlock, Active: r.Active, CreatedAt: r.CreatedAt,
		LiftedBy: r.LiftedBy.String,
	}
	if r.LiftedAt.Valid {
		b.LiftedAt = r.LiftedAt.Time
	}
	return b
}

func optInt8(p *int64) pgtype.Int8 {
	if p == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *p, Valid: true}
}

// isUniqueViolation reports whether err is a Postgres unique-constraint violation.
func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func ptr8(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
