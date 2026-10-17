package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Bot is one app.bots row (DESIGN.md §14): a paired number and its connector lease.
type Bot struct {
	ID          int64
	JID         wa.JID
	LID         wa.JID
	DisplayName string
	Status      string // active | paused | banned | unpaired
	LeaseOwner  string
	LeaseUntil  time.Time
}

// Bots is the bots table. Its lease methods match the connector's Lease interface: a process
// connects a bot only while it holds the bot's row lease (DESIGN.md §15), and the database clock
// decides expiry so processes on different hosts agree.
type Bots struct{ q *db.Queries }

// NewBots builds the repository on q (a pool or a transaction).
func NewBots(q db.DBTX) *Bots { return &Bots{q: db.New(q)} }

// UpsertBot records a paired device. An existing row keeps its status and lease.
func (b *Bots) UpsertBot(ctx context.Context, jid, lid wa.JID, displayName, status string) error {
	err := b.q.UpsertBot(ctx, db.UpsertBotParams{
		Jid: string(jid), Lid: pgtype.Text{String: string(lid), Valid: lid != ""}, DisplayName: displayName, Status: status,
	})
	if err != nil {
		return fmt.Errorf("upsert bot: %w", err)
	}
	return nil
}

// SetBotStatus changes a bot's status.
func (b *Bots) SetBotStatus(ctx context.Context, jid wa.JID, status string) error {
	n, err := b.q.SetBotStatus(ctx, db.SetBotStatusParams{Jid: string(jid), Status: status})
	if err != nil {
		return fmt.Errorf("set bot status: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("set bot status: no bot %s", jid)
	}
	return nil
}

// Bot reads one bot.
func (b *Bots) Bot(ctx context.Context, jid wa.JID) (Bot, bool, error) {
	r, err := b.q.GetBot(ctx, string(jid))
	if errors.Is(err, pgx.ErrNoRows) {
		return Bot{}, false, nil
	}
	if err != nil {
		return Bot{}, false, fmt.Errorf("get bot: %w", err)
	}
	return botFromRow(db.ListBotsRow(r)), true, nil
}

// List returns every bot.
func (b *Bots) List(ctx context.Context) ([]Bot, error) {
	rows, err := b.q.ListBots(ctx)
	if err != nil {
		return nil, fmt.Errorf("list bots: %w", err)
	}
	out := make([]Bot, 0, len(rows))
	for _, r := range rows {
		out = append(out, botFromRow(r))
	}
	return out, nil
}

func botFromRow(r db.ListBotsRow) Bot {
	bot := Bot{
		ID: r.ID, JID: wa.JID(r.Jid), LID: wa.JID(r.Lid.String), DisplayName: r.DisplayName,
		Status: r.Status, LeaseOwner: r.LeaseOwner.String,
	}
	if r.LeaseUntil.Valid {
		bot.LeaseUntil = r.LeaseUntil.Time
	}
	return bot
}

// Acquire takes the bot's lease for ttl when it is free, expired or already owner's. A bot
// without a row can't be leased.
func (b *Bots) Acquire(ctx context.Context, bot wa.JID, owner string, ttl time.Duration) (bool, error) {
	if owner == "" {
		return false, errors.New("acquire lease: empty owner")
	}
	n, err := b.q.AcquireBotLease(ctx, db.AcquireBotLeaseParams{Jid: string(bot), Owner: pgtype.Text{String: owner, Valid: true}, TtlSeconds: ttl.Seconds()})
	if err != nil {
		return false, fmt.Errorf("acquire lease: %w", err)
	}
	return n == 1, nil
}

// Renew extends a lease owner still holds; false means it was lost (expired and taken, or
// released).
func (b *Bots) Renew(ctx context.Context, bot wa.JID, owner string, ttl time.Duration) (bool, error) {
	n, err := b.q.RenewBotLease(ctx, db.RenewBotLeaseParams{Jid: string(bot), Owner: pgtype.Text{String: owner, Valid: true}, TtlSeconds: ttl.Seconds()})
	if err != nil {
		return false, fmt.Errorf("renew lease: %w", err)
	}
	return n == 1, nil
}

// Release gives the lease up if owner holds it.
func (b *Bots) Release(ctx context.Context, bot wa.JID, owner string) error {
	if err := b.q.ReleaseBotLease(ctx, db.ReleaseBotLeaseParams{Jid: string(bot), Owner: pgtype.Text{String: owner, Valid: true}}); err != nil {
		return fmt.Errorf("release lease: %w", err)
	}
	return nil
}
