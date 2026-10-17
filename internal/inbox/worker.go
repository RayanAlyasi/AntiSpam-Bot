package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// NotifyChannel is the LISTEN/NOTIFY channel an inbox insert signals.
const NotifyChannel = "app_inbox"

// Item is one claimed inbox row.
type Item struct {
	ID         int64
	Bot        wa.JID
	Chat       wa.JID
	MsgID      wa.MessageID
	EditSeq    string
	Kind       string // KindMessage or KindUndecryptable
	Sender     wa.JID
	Raw        []byte          // the marshalled waE2E.Message (nil for undecryptable)
	Info       json.RawMessage // whatsmeow's MessageInfo
	ReceivedAt time.Time
	Attempts   int // including this one
}

// Handler processes one inbox item. A nil error marks the row processed. An error leaves it for
// a retry with backoff, up to the worker's attempt limit; ErrPermanent marks it processed at once.
type Handler interface {
	HandleInbox(ctx context.Context, it Item) error
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ctx context.Context, it Item) error

// HandleInbox calls f.
func (f HandlerFunc) HandleInbox(ctx context.Context, it Item) error { return f(ctx, it) }

// ErrPermanent wraps an error that retrying won't fix (a malformed row): the row is marked
// processed with the error recorded.
var ErrPermanent = errors.New("permanent inbox error")

// WorkerOptions tunes a Worker.
type WorkerOptions struct {
	Workers     int           // concurrent claimers (default 4)
	Batch       int           // rows per claim (default 16)
	Claim       time.Duration // claim lease; a crashed worker's rows come back after it (default 5 min)
	MaxAttempts int           // attempts before a row is given up (default 5)
	Poll        time.Duration // fallback poll when no NOTIFY arrives (default 5 s)
	Clock       clock.Clock
	Log         zerolog.Logger
}

// Worker claims unprocessed inbox rows with FOR UPDATE SKIP LOCKED, so any number of workers in
// any number of processes share the work, wakes on LISTEN/NOTIFY, and resumes rows a crashed
// worker left behind once their claim expires (DESIGN.md §5.1, §15).
type Worker struct {
	pool    *pgxpool.Pool
	q       *db.Queries
	handler Handler
	opts    WorkerOptions
	wake    chan struct{}
}

// NewWorker builds a worker.
func NewWorker(pool *pgxpool.Pool, h Handler, opts WorkerOptions) *Worker {
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.Batch <= 0 {
		opts.Batch = 16
	}
	if opts.Claim <= 0 {
		opts.Claim = 5 * time.Minute
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 5
	}
	if opts.Poll <= 0 {
		opts.Poll = 5 * time.Second
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	opts.Log = opts.Log.With().Str("module", "inbox").Logger()
	return &Worker{pool: pool, q: db.New(pool), handler: h, opts: opts, wake: make(chan struct{}, 1)}
}

// Run processes the inbox until ctx ends.
func (w *Worker) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	wg.Go(func() { w.listen(ctx) })
	for range w.opts.Workers {
		wg.Go(func() { w.loop(ctx) })
	}
	wg.Wait()
	return nil
}

// listen turns NOTIFYs into wake-ups. If the listening connection fails it retries; the poll
// keeps the workers going meanwhile.
func (w *Worker) listen(ctx context.Context) {
	for ctx.Err() == nil {
		if err := w.listenOnce(ctx); err != nil && ctx.Err() == nil {
			w.opts.Log.Warn().Err(err).Msg("inbox LISTEN failed; retrying")
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (w *Worker) listenOnce(ctx context.Context) error {
	conn, err := w.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire listen connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+NotifyChannel); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "UNLISTEN "+NotifyChannel) }()
	for {
		if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
			return fmt.Errorf("wait for notification: %w", err)
		}
		w.signal()
	}
}

func (w *Worker) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Worker) loop(ctx context.Context) {
	for ctx.Err() == nil {
		n, err := w.ProcessBatch(ctx)
		if err != nil && ctx.Err() == nil {
			w.opts.Log.Error().Err(err).Msg("claim inbox rows")
		}
		if n > 0 && err == nil {
			w.signal() // there may be more: let the other workers look too
			continue
		}
		select {
		case <-ctx.Done():
		case <-w.wake:
		case <-time.After(w.opts.Poll):
		}
	}
}

// ProcessBatch claims one batch and handles it, returning how many rows it claimed.
func (w *Worker) ProcessBatch(ctx context.Context) (int, error) {
	rows, err := w.q.ClaimInbox(ctx, db.ClaimInboxParams{ClaimSeconds: w.opts.Claim.Seconds(), RowLimit: int32(w.opts.Batch)})
	if err != nil {
		return 0, fmt.Errorf("claim: %w", err)
	}
	for _, r := range rows {
		if ctx.Err() != nil {
			// Shutting down: hand the rest of the batch back without spending their attempts.
			w.unclaim(r.ID)
			continue
		}
		it := Item{
			ID: r.ID, Bot: wa.JID(r.BotJid), Chat: wa.JID(r.Chat), MsgID: wa.MessageID(r.MsgID), EditSeq: r.EditSeq,
			Kind: r.Kind, Sender: wa.JID(r.Sender), Raw: r.Raw, Info: r.Info, ReceivedAt: r.ReceivedAt, Attempts: int(r.Attempts),
		}
		err := w.handle(ctx, it)
		if err != nil && ctx.Err() != nil {
			w.unclaim(it.ID) // failed because we're stopping, not because of the row
			continue
		}
		w.finish(ctx, it, err)
	}
	return len(rows), nil
}

func (w *Worker) unclaim(id int64) {
	if err := w.q.UnclaimInbox(context.Background(), id); err != nil {
		w.opts.Log.Error().Err(err).Int64("inbox_id", id).Msg("hand back inbox row")
	}
}

func (w *Worker) handle(ctx context.Context, it Item) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%w: handler panicked: %v", ErrPermanent, p)
		}
	}()
	return w.handler.HandleInbox(ctx, it)
}

func (w *Worker) finish(ctx context.Context, it Item, herr error) {
	ctx = context.WithoutCancel(ctx) // record the outcome even while shutting down
	now := w.opts.Clock.Now()
	log := w.opts.Log.With().Int64("inbox_id", it.ID).Str("msg_id", string(it.MsgID)).Logger()
	switch {
	case herr == nil:
		if err := w.q.MarkInboxProcessed(ctx, db.MarkInboxProcessedParams{ID: it.ID, ProcessedAt: pgtype.Timestamptz{Time: now, Valid: true}}); err != nil {
			log.Error().Err(err).Msg("mark inbox row processed")
		}
	case errors.Is(herr, ErrPermanent) || it.Attempts >= w.opts.MaxAttempts:
		log.Error().Err(herr).Int("attempts", it.Attempts).Msg("inbox row given up")
		msg := herr.Error()
		if err := w.q.MarkInboxProcessed(ctx, db.MarkInboxProcessedParams{ID: it.ID, ProcessedAt: pgtype.Timestamptz{Time: now, Valid: true}, LastError: textOf(msg)}); err != nil {
			log.Error().Err(err).Msg("mark inbox row failed")
		}
	default:
		backoff := min(time.Duration(1<<it.Attempts)*2*time.Second, 5*time.Minute)
		log.Warn().Err(herr).Int("attempts", it.Attempts).Dur("retry_in", backoff).Msg("inbox row failed; will retry")
		if err := w.q.ReleaseInboxClaim(ctx, db.ReleaseInboxClaimParams{ID: it.ID, RetrySeconds: backoff.Seconds(), LastError: textOf(herr.Error())}); err != nil {
			log.Error().Err(err).Msg("release inbox claim")
		}
	}
}
