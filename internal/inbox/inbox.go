package inbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Inbox row kinds.
const (
	KindMessage       = "message"
	KindUndecryptable = "undecryptable"
)

// Retention defaults (DESIGN.md §14, §17).
const (
	InboxRetention       = 72 * time.Hour
	IndexRetention       = 7 * 24 * time.Hour
	IndexActionRetention = 30 * 24 * time.Hour
)

// Writer stores message events in the inbox. It is the connector's sink for message events: a
// nil error means the row is committed (or was already there), so the message may be acked.
type Writer struct {
	q   *db.Queries
	clk clock.Clock
}

// NewWriter builds a writer on the pool.
func NewWriter(pool *pgxpool.Pool, clk clock.Clock) *Writer {
	if clk == nil {
		clk = clock.Real{}
	}
	return &Writer{q: db.New(pool), clk: clk}
}

// HandleEvent inserts message and undecryptable events; other events are ignored. A redelivered
// event (same chat, message ID, edit attribute and kind) is a no-op, not an error.
func (w *Writer) HandleEvent(ctx context.Context, evt wa.Event) error {
	var (
		info wa.MessageInfo
		raw  []byte
		js   []byte
		kind string
	)
	switch e := evt.(type) {
	case *wa.MessageEvent:
		info, raw, js, kind = e.Info, e.Raw, e.InfoJSON, KindMessage
	case *wa.UndecryptableEvent:
		info, js, kind = e.Info, e.InfoJSON, KindUndecryptable
	default:
		return nil
	}
	if !json.Valid(js) {
		return fmt.Errorf("inbox insert %s: info is not JSON", info.ID)
	}
	_, err := w.q.InsertInbox(ctx, db.InsertInboxParams{
		BotJid: string(evt.BotJID()), Chat: string(info.Chat), MsgID: string(info.ID), EditSeq: info.Edit,
		Kind: kind, Sender: string(info.Sender), Raw: raw, Info: js, ReceivedAt: w.clk.Now(),
	})
	if err != nil {
		return fmt.Errorf("inbox insert %s: %w", info.ID, err)
	}
	return nil
}

// Purge deletes inbox rows received more than InboxRetention ago and expired message_index rows.
func Purge(ctx context.Context, pool *pgxpool.Pool, now time.Time) (inboxRows, indexRows int64, err error) {
	q := db.New(pool)
	if inboxRows, err = q.DeleteInboxBefore(ctx, now.Add(-InboxRetention)); err != nil {
		return 0, 0, fmt.Errorf("purge inbox: %w", err)
	}
	if indexRows, err = q.DeleteExpiredMessageIndex(ctx, now); err != nil {
		return inboxRows, 0, fmt.Errorf("purge message_index: %w", err)
	}
	return inboxRows, indexRows, nil
}
