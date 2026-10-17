// Package pgstore is the Postgres implementation of the review package's stores: reviews with
// their votes and posted messages, evidence snapshots, DM threads and the audit log
// (DESIGN.md §9.5, §13.6, §14).
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// RawTextRetention is how long a review may hold the unredacted message text while it waits
// for a decision: the raw inbox's 72 h (§17).
const RawTextRetention = 72 * time.Hour

// uniqueViolation is Postgres' SQLSTATE for a unique-constraint violation.
const uniqueViolation = "23505"

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()} }

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return review.ErrNotFound
	}
	return err
}

// subjectDoc and evidenceDoc are the JSON columns. Evidence text is stored redacted; the raw
// text sits in evidence_raw only until the decision, which hands it to the corpus once (§7.5, §17).
type subjectDoc struct {
	Person          wa.JID        `json:"person,omitempty"`
	Phone           wa.JID        `json:"phone,omitempty"`
	Name            string        `json:"name,omitempty"`
	MemberFor       time.Duration `json:"member_for,omitempty"`
	EarlierMessages int           `json:"earlier_messages,omitempty"`
	Established     bool          `json:"established,omitempty"`
	Hacked          bool          `json:"hacked,omitempty"`
	Admin           bool          `json:"admin,omitempty"`
}

type evidenceDoc struct {
	Text           string          `json:"text,omitempty"`
	Links          []string        `json:"links,omitempty"`
	Why            []review.Reason `json:"why,omitempty"`
	Score          float64         `json:"score,omitempty"`
	Source         string          `json:"source,omitempty"`
	MessageDeleted bool            `json:"message_deleted,omitempty"`
	WhyAsking      string          `json:"why_asking,omitempty"`
}

// Reviews is a review.Store on Postgres.
type Reviews struct {
	DB db.DBTX
}

var _ review.Store = Reviews{}

func (r Reviews) q() *db.Queries { return db.New(r.DB) }

// Create stores a new review. Evidence text is redacted before it's written.
func (r Reviews) Create(ctx context.Context, it review.Item) (review.Item, error) {
	sd := subjectDoc(it.Subject)
	sd.Name = review.Redact(sd.Name)
	subj, err := json.Marshal(sd)
	if err != nil {
		return review.Item{}, fmt.Errorf("encode subject: %w", err)
	}
	ev := it.Evidence
	evDoc := evidenceDoc{
		Text: review.Redact(ev.Text), Links: ev.Links, Why: ev.Why, Score: ev.Score, Source: ev.Source,
		MessageDeleted: ev.MessageDeleted, WhyAsking: ev.WhyAsking,
	}
	evJSON, err := json.Marshal(evDoc)
	if err != nil {
		return review.Item{}, fmt.Errorf("encode evidence: %w", err)
	}
	p := db.InsertReviewParams{
		ShortCode: it.Code, GroupJid: string(it.Group), GroupName: it.GroupName, Kind: string(it.Kind),
		DetectionID: pgtype.Int8{Int64: it.DetectionID, Valid: it.DetectionID != 0},
		Subject:     subj, Evidence: evJSON, Proposed: string(it.Proposed), Mode: string(it.Mode),
		Status: string(it.Status), Deadline: it.Deadline, OnTimeout: string(it.OnTimeout), CreatedAt: it.CreatedAt,
	}
	if it.Message != nil {
		p.MessageSender, p.MessageID = text(string(it.Message.Sender)), text(string(it.Message.ID))
	}
	p.EvidenceRaw = text(ev.Text)
	if ev.Thumbnail != nil && len(ev.Thumbnail.Data) > 0 {
		p.Thumbnail, p.ThumbnailMime = ev.Thumbnail.Data, text(ev.Thumbnail.Mimetype)
	}
	if p.Status == "" {
		p.Status = string(review.StatusPending)
	}
	if p.CreatedAt.IsZero() || p.Deadline.IsZero() {
		return review.Item{}, errors.New("insert review: created_at and deadline are required")
	}
	id, err := r.q().InsertReview(ctx, p)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return review.Item{}, review.ErrCodeTaken
	}
	if err != nil {
		return review.Item{}, fmt.Errorf("insert review: %w", err)
	}
	return r.Get(ctx, id)
}

func itemOf(row db.AppReview) (review.Item, error) {
	var subj subjectDoc
	if err := json.Unmarshal(row.Subject, &subj); err != nil {
		return review.Item{}, fmt.Errorf("decode subject of review %d: %w", row.ID, err)
	}
	var ev evidenceDoc
	if err := json.Unmarshal(row.Evidence, &ev); err != nil {
		return review.Item{}, fmt.Errorf("decode evidence of review %d: %w", row.ID, err)
	}
	it := review.Item{
		NewItem: review.NewItem{
			Group: wa.JID(row.GroupJid), GroupName: row.GroupName, Kind: review.Kind(row.Kind),
			Subject: review.Subject(subj), Proposed: review.Decision(row.Proposed), DetectionID: row.DetectionID.Int64,
			Evidence: review.Evidence{
				Text: ev.Text, Links: ev.Links, Why: ev.Why, Score: ev.Score, Source: ev.Source,
				MessageDeleted: ev.MessageDeleted, WhyAsking: ev.WhyAsking,
			},
		},
		ID: row.ID, Code: row.ShortCode, Status: review.Status(row.Status), Mode: review.EnforcementMode(row.Mode),
		Deadline: row.Deadline, OnTimeout: review.OnTimeout(row.OnTimeout), CreatedAt: row.CreatedAt,
		Decision: review.Decision(row.Decision.String), DecidedBy: wa.JID(row.DecidedBy.String),
		DecidedByName: row.DecidedByName.String, Method: review.Method(row.Method.String), DecidedAt: row.DecidedAt.Time,
	}
	if row.MessageID.Valid {
		it.Message = &wa.MessageRef{Sender: wa.JID(row.MessageSender.String), ID: wa.MessageID(row.MessageID.String)}
	}
	if len(row.Thumbnail) > 0 {
		it.Evidence.Thumbnail = &wa.Image{Data: row.Thumbnail, Mimetype: row.ThumbnailMime.String}
	}
	return it, nil
}

func itemsOf(rows []db.AppReview) ([]review.Item, error) {
	out := make([]review.Item, 0, len(rows))
	for _, row := range rows {
		it, err := itemOf(row)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, nil
}

// Get returns a review by ID.
func (r Reviews) Get(ctx context.Context, id int64) (review.Item, error) {
	row, err := r.q().GetReview(ctx, id)
	if err != nil {
		return review.Item{}, fmt.Errorf("get review %d: %w", id, notFound(err))
	}
	return itemOf(row)
}

// ByCode returns a review by short code.
func (r Reviews) ByCode(ctx context.Context, code string) (review.Item, error) {
	row, err := r.q().GetReviewByCode(ctx, code)
	if err != nil {
		return review.Item{}, fmt.Errorf("get review %s: %w", code, notFound(err))
	}
	return itemOf(row)
}

// Decide moves a pending review to its decision in one conditional UPDATE: the first decision
// wins, even across processes (§9.5).
func (r Reviews) Decide(ctx context.Context, id int64, d review.Decided) (review.Item, bool, error) {
	row, err := r.q().DecideReview(ctx, db.DecideReviewParams{
		ID: id, Status: string(d.Status), Decision: text(string(d.Decision)), DecidedBy: text(string(d.By)),
		DecidedByName: text(review.Redact(d.ByName)), Method: text(string(d.Method)), DecidedAt: ts(d.At),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		cur, err := r.Get(ctx, id)
		return cur, false, err
	}
	if err != nil {
		return review.Item{}, false, fmt.Errorf("decide review %d: %w", id, err)
	}
	it, err := r.Get(ctx, row.ID)
	if err != nil {
		return review.Item{}, false, err
	}
	if row.RawText.Valid && d.Decision != review.DecisionNotSpam {
		// The winning decision gets the unredacted text once, so confirmed spam reaches the
		// corpus intact; the row no longer holds it.
		it.Evidence.Text = row.RawText.String
	}
	return it, true, nil
}

// AddVote stores a vote.
func (r Reviews) AddVote(ctx context.Context, v review.VoteRecord) error {
	err := r.q().InsertReviewVote(ctx, db.InsertReviewVoteParams{
		ReviewID: v.ReviewID, Voter: string(v.Voter), Method: string(v.Method), Raw: v.Raw,
		Decision: string(v.Decision), Valid: v.Valid, InvalidReason: v.InvalidReason, At: v.At,
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return review.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("insert vote on review %d: %w", v.ReviewID, err)
	}
	return nil
}

// Votes lists a review's votes in order.
func (r Reviews) Votes(ctx context.Context, id int64) ([]review.VoteRecord, error) {
	rows, err := r.q().ListReviewVotes(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("votes on review %d: %w", id, err)
	}
	out := make([]review.VoteRecord, len(rows))
	for i, v := range rows {
		out[i] = review.VoteRecord{
			ReviewID: v.ReviewID, Voter: wa.JID(v.Voter), Method: review.Method(v.Method), Raw: v.Raw,
			Decision: review.Decision(v.Decision), Valid: v.Valid, InvalidReason: v.InvalidReason, At: v.At,
		}
	}
	return out, nil
}

// AddPosted maps one of the bot's review messages to its review.
func (r Reviews) AddPosted(ctx context.Context, p review.Posted) error {
	err := r.q().InsertReviewMessage(ctx, db.InsertReviewMessageParams{
		ChatJid: string(p.Chat), MsgID: string(p.ID), ReviewID: p.ReviewID, SentAt: p.SentAt,
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return review.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("insert review message %s: %w", p.ID, err)
	}
	return nil
}

// PostedFor lists a review's posted messages, oldest first.
func (r Reviews) PostedFor(ctx context.Context, id int64) ([]review.Posted, error) {
	rows, err := r.q().ListReviewMessages(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("messages of review %d: %w", id, err)
	}
	out := make([]review.Posted, len(rows))
	for i, m := range rows {
		out[i] = review.Posted{ReviewID: m.ReviewID, Chat: wa.JID(m.ChatJid), ID: wa.MessageID(m.MsgID), SentAt: m.SentAt, OutcomeEdited: m.OutcomeEdited}
	}
	return out, nil
}

// ByMessage finds the review a bot message belongs to.
func (r Reviews) ByMessage(ctx context.Context, chat wa.JID, id wa.MessageID) (review.Item, error) {
	row, err := r.q().GetReviewByMessage(ctx, db.GetReviewByMessageParams{ChatJid: string(chat), MsgID: string(id)})
	if err != nil {
		return review.Item{}, fmt.Errorf("review for message %s: %w", id, notFound(err))
	}
	return itemOf(row)
}

// MarkOutcomeEdited records that the outcome edit was sent.
func (r Reviews) MarkOutcomeEdited(ctx context.Context, chat wa.JID, id wa.MessageID) error {
	n, err := r.q().MarkReviewOutcomeEdited(ctx, db.MarkReviewOutcomeEditedParams{ChatJid: string(chat), MsgID: string(id)})
	if err != nil {
		return fmt.Errorf("mark outcome edited %s: %w", id, err)
	}
	if n == 0 {
		return review.ErrNotFound
	}
	return nil
}

// Pending lists pending reviews of a group (all groups when empty), oldest first.
func (r Reviews) Pending(ctx context.Context, group wa.JID) ([]review.Item, error) {
	rows, err := r.q().ListPendingReviews(ctx, string(group))
	if err != nil {
		return nil, fmt.Errorf("pending reviews: %w", err)
	}
	return itemsOf(rows)
}

// Due lists pending reviews past their deadline.
func (r Reviews) Due(ctx context.Context, now time.Time) ([]review.Item, error) {
	rows, err := r.q().ListDueReviews(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("due reviews: %w", err)
	}
	return itemsOf(rows)
}

// Evidence is a review.EvidenceStore on Postgres.
type Evidence struct {
	DB db.DBTX
}

var _ review.EvidenceStore = Evidence{}

// Save stores or replaces a snapshot.
func (e Evidence) Save(ctx context.Context, s review.Snapshot) error {
	p := db.UpsertEvidenceSnapshotParams{
		GroupJid: string(s.Group), MsgID: string(s.Message.ID), Sender: string(s.Message.Sender), Text: s.Text,
		DetectionID: pgtype.Int8{Int64: s.DetectionID, Valid: s.DetectionID != 0}, Reason: s.Reason,
		At: s.At, ExpiresAt: ts(s.ExpiresAt),
	}
	if s.Thumbnail != nil && len(s.Thumbnail.Data) > 0 {
		p.Thumbnail, p.ThumbnailMime = s.Thumbnail.Data, text(s.Thumbnail.Mimetype)
	}
	if err := db.New(e.DB).UpsertEvidenceSnapshot(ctx, p); err != nil {
		return fmt.Errorf("save evidence %s: %w", s.Message.ID, err)
	}
	return nil
}

// Get returns a snapshot.
func (e Evidence) Get(ctx context.Context, group wa.JID, msg wa.MessageID) (review.Snapshot, error) {
	row, err := db.New(e.DB).GetEvidenceSnapshot(ctx, db.GetEvidenceSnapshotParams{GroupJid: string(group), MsgID: string(msg)})
	if err != nil {
		return review.Snapshot{}, fmt.Errorf("evidence %s: %w", msg, notFound(err))
	}
	s := review.Snapshot{
		Group: wa.JID(row.GroupJid), Message: wa.MessageRef{Sender: wa.JID(row.Sender), ID: wa.MessageID(row.MsgID)},
		Text: row.Text, DetectionID: row.DetectionID.Int64, Reason: row.Reason, At: row.At, ExpiresAt: row.ExpiresAt.Time,
	}
	if len(row.Thumbnail) > 0 {
		s.Thumbnail = &wa.Image{Data: row.Thumbnail, Mimetype: row.ThumbnailMime.String}
	}
	return s, nil
}

// DeleteText clears a message's text and thumbnail everywhere it is kept: its snapshot and any
// review about it (§7.5: no text of an overturned message remains).
func (e Evidence) DeleteText(ctx context.Context, group wa.JID, msg wa.MessageID) error {
	q := db.New(e.DB)
	n, err := q.ClearEvidenceText(ctx, db.ClearEvidenceTextParams{GroupJid: string(group), MsgID: string(msg)})
	if err != nil {
		return fmt.Errorf("clear evidence text %s: %w", msg, err)
	}
	m, err := q.ClearReviewEvidenceText(ctx, db.ClearReviewEvidenceTextParams{GroupJid: string(group), MsgID: pgtype.Text{String: string(msg), Valid: true}})
	if err != nil {
		return fmt.Errorf("clear review text %s: %w", msg, err)
	}
	if n+m == 0 {
		return review.ErrNotFound
	}
	return nil
}

// Purge deletes expired snapshots and strips the text and thumbnail from reviews older than
// the evidence retention (§14, §17.1). It returns how many snapshots it deleted.
func (e Evidence) Purge(ctx context.Context, now time.Time) (int, error) {
	q := db.New(e.DB)
	n, err := q.PurgeEvidence(ctx, ts(now))
	if err != nil {
		return 0, fmt.Errorf("purge evidence: %w", err)
	}
	if _, err := q.ClearStaleReviewRawText(ctx, now.Add(-RawTextRetention)); err != nil {
		return int(n), fmt.Errorf("clear stale raw review text: %w", err)
	}
	if _, err := q.ExpireReviewEvidence(ctx, now.Add(-review.EvidenceRetention)); err != nil {
		return int(n), fmt.Errorf("expire review evidence: %w", err)
	}
	return int(n), nil
}

// Threads is the DM thread store (chat.ThreadStore) on Postgres.
type Threads struct {
	DB db.DBTX
}

// MarkInbound records an inbound DM.
func (t Threads) MarkInbound(ctx context.Context, person wa.JID, at time.Time) error {
	if err := db.New(t.DB).MarkDMInbound(ctx, db.MarkDMInboundParams{Person: string(person), At: ts(at)}); err != nil {
		return fmt.Errorf("mark inbound DM: %w", err)
	}
	return nil
}

// LastInbound returns when the person last wrote to the bot.
func (t Threads) LastInbound(ctx context.Context, person wa.JID) (time.Time, bool, error) {
	row, err := db.New(t.DB).GetDMThread(ctx, string(person))
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("DM thread: %w", err)
	}
	return row.LastInboundAt.Time, row.LastInboundAt.Valid, nil
}

// MarkException records a command reply sent under the once-a-day exception.
func (t Threads) MarkException(ctx context.Context, person wa.JID, at time.Time) error {
	if err := db.New(t.DB).MarkDMException(ctx, db.MarkDMExceptionParams{Person: string(person), At: ts(at)}); err != nil {
		return fmt.Errorf("mark DM exception: %w", err)
	}
	return nil
}

// LastException returns when the exception was last used.
func (t Threads) LastException(ctx context.Context, person wa.JID) (time.Time, bool, error) {
	row, err := db.New(t.DB).GetDMThread(ctx, string(person))
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("DM thread: %w", err)
	}
	return row.LastExceptionAt.Time, row.LastExceptionAt.Valid, nil
}

// Audit is a review.Audit on the audit_log table (F-006).
type Audit struct {
	DB db.DBTX
}

var _ review.Audit = Audit{}

// Record writes one audit entry; an entry without an actor is the bot's own.
func (a Audit) Record(ctx context.Context, e review.AuditEntry) error {
	diff, err := json.Marshal(e.Diff)
	if err != nil {
		return fmt.Errorf("encode audit diff: %w", err)
	}
	if e.Diff == nil {
		diff = []byte(`{}`)
	}
	actor := string(e.Actor)
	if actor == "" {
		actor = store.ActorSystem
	}
	_, err = store.WriteAudit(ctx, a.DB, store.AuditEntry{Actor: actor, GroupJID: string(e.Group), Action: e.Action, Diff: diff})
	return err
}

// BlacklistBans records which local ban the blacklist placed, by ban ID (queue.BlacklistBans).
type BlacklistBans struct {
	DB db.DBTX
}

// Mark records the blacklist's ban.
func (b BlacklistBans) Mark(ctx context.Context, group wa.JID, personID, banID int64) error {
	if err := db.New(b.DB).MarkBlacklistBan(ctx, db.MarkBlacklistBanParams{GroupJid: string(group), PersonID: personID, BanID: banID}); err != nil {
		return fmt.Errorf("mark blacklist ban: %w", err)
	}
	return nil
}

// BanID returns the ban the blacklist placed for the person in group, if any.
func (b BlacklistBans) BanID(ctx context.Context, group wa.JID, personID int64) (int64, bool, error) {
	id, err := db.New(b.DB).GetBlacklistBan(ctx, db.GetBlacklistBanParams{GroupJid: string(group), PersonID: personID})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("blacklist ban lookup: %w", err)
	}
	return id, true, nil
}

// Forget drops the record once the ban is lifted.
func (b BlacklistBans) Forget(ctx context.Context, group wa.JID, personID int64) error {
	if err := db.New(b.DB).ForgetBlacklistBan(ctx, db.ForgetBlacklistBanParams{GroupJid: string(group), PersonID: personID}); err != nil {
		return fmt.Errorf("forget blacklist ban: %w", err)
	}
	return nil
}
