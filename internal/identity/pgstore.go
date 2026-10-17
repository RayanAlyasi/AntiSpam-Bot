package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// NewPostgresStore returns the Store on app.persons, app.identities, app.person_merges and
// app.members. Writes take a transaction-scoped advisory lock, so identity writes from every
// process are serialized.
func NewPostgresStore(pool *pgxpool.Pool) Store {
	return &pgStore{pool: pool, pgReader: pgReader{q: db.New(pool)}}
}

type pgStore struct {
	pgReader
	pool *pgxpool.Pool
}

func (s *pgStore) Write(ctx context.Context, fn func(tx Tx) error) error {
	return store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := q.LockIdentityWrites(ctx); err != nil {
			return fmt.Errorf("lock identity writes: %w", err)
		}
		return fn(&pgTx{pgReader: pgReader{q: q}, tx: tx})
	})
}

type pgReader struct{ q *db.Queries }

func (r pgReader) FindOpen(ctx context.Context, kind Kind, value wa.JID) (PersonID, bool, error) {
	id, err := r.q.FindOpenIdentity(ctx, db.FindOpenIdentityParams{Kind: string(kind), Value: string(value)})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("find identity: %w", err)
	}
	return PersonID(id), true, nil
}

func (r pgReader) Person(ctx context.Context, id PersonID) (Person, error) {
	row, err := r.q.GetPerson(ctx, int64(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Person{}, fmt.Errorf("person %d: %w", id, ErrNoPerson)
	}
	if err != nil {
		return Person{}, fmt.Errorf("get person %d: %w", id, err)
	}
	return Person{ID: PersonID(row.ID), LID: wa.JID(row.Lid.String), PN: wa.JID(row.Pn.String)}, nil
}

func (r pgReader) Identities(ctx context.Context, id PersonID) ([]Identity, error) {
	rows, err := r.q.ListIdentities(ctx, int64(id))
	if err != nil {
		return nil, fmt.Errorf("list identities of %d: %w", id, err)
	}
	out := make([]Identity, 0, len(rows))
	for _, row := range rows {
		ident := Identity{Kind: Kind(row.Kind), Value: wa.JID(row.Value), Source: Source(row.Source), CountryCode: row.CountryCode.String}
		if row.ValidTo.Valid {
			t := row.ValidTo.Time
			ident.ValidTo = &t
		}
		out = append(out, ident)
	}
	return out, nil
}

func (r pgReader) Member(ctx context.Context, group wa.JID, id PersonID) (Member, bool, error) {
	row, err := r.q.GetMember(ctx, db.GetMemberParams{GroupJid: string(group), PersonID: int64(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Member{}, false, nil
	}
	if err != nil {
		return Member{}, false, fmt.Errorf("get member: %w", err)
	}
	return memberFromRow(row), true, nil
}

func (r pgReader) Members(ctx context.Context, id PersonID) ([]Member, error) {
	rows, err := r.q.ListMembersByPerson(ctx, int64(id))
	if err != nil {
		return nil, fmt.Errorf("list members of person %d: %w", id, err)
	}
	return membersFromRows(rows), nil
}

func (r pgReader) GroupMembers(ctx context.Context, group wa.JID) ([]Member, error) {
	rows, err := r.q.ListMembersByGroup(ctx, string(group))
	if err != nil {
		return nil, fmt.Errorf("list members of %s: %w", group, err)
	}
	return membersFromRows(rows), nil
}

func memberFromRow(r db.AppMember) Member {
	m := Member{
		Group: wa.JID(r.GroupJid), Person: PersonID(r.PersonID), Role: Role(r.Role), FirstSeen: r.FirstSeen,
		MsgCount: int(r.MsgCount), CleanCount: int(r.CleanCount), PreExisting: r.PreExisting,
		Established: r.Established, Allowlisted: r.Allowlisted,
	}
	if r.JoinedAt.Valid {
		m.JoinedAt = r.JoinedAt.Time
	}
	return m
}

func membersFromRows(rows []db.AppMember) []Member {
	out := make([]Member, 0, len(rows))
	for _, r := range rows {
		out = append(out, memberFromRow(r))
	}
	return out
}

type pgTx struct {
	pgReader
	tx pgx.Tx
}

func (t *pgTx) CreatePerson(ctx context.Context, now time.Time) (PersonID, error) {
	id, err := t.q.CreatePerson(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("create person: %w", err)
	}
	return PersonID(id), nil
}

func (t *pgTx) SetIdentifiers(ctx context.Context, p Person) error {
	n, err := t.q.SetPersonIdentifiers(ctx, db.SetPersonIdentifiersParams{
		ID: int64(p.ID), Lid: text(string(p.LID)), Pn: text(string(p.PN)), PnCountry: text(CountryCode(p.PN)),
	})
	if err != nil {
		return fmt.Errorf("set identifiers of %d: %w", p.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("set identifiers of %d: %w", p.ID, ErrNoPerson)
	}
	return nil
}

func (t *pgTx) AddIdentity(ctx context.Context, id PersonID, ident Identity, now time.Time) error {
	owner, err := t.q.TouchOpenIdentity(ctx, db.TouchOpenIdentityParams{LastSeen: now, Kind: string(ident.Kind), Value: string(ident.Value)})
	switch {
	case err == nil && PersonID(owner) != id:
		return fmt.Errorf("identity %s is linked to person %d: %w", ident.Value, owner, ErrConflict)
	case err == nil:
		return nil
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("touch identity: %w", err)
	}
	var prefix string
	if ident.Kind == KindPN {
		prefix = NumberPrefix(ident.Value)
	}
	err = t.q.InsertIdentity(ctx, db.InsertIdentityParams{
		PersonID: int64(id), Kind: string(ident.Kind), Value: string(ident.Value),
		CountryCode: text(ident.CountryCode), NumberPrefix: text(prefix), Source: string(ident.Source), Seen: now,
	})
	if err != nil {
		return fmt.Errorf("insert identity: %w", err)
	}
	return nil
}

func (t *pgTx) CloseIdentity(ctx context.Context, kind Kind, value wa.JID, at time.Time) error {
	if err := t.q.CloseIdentity(ctx, db.CloseIdentityParams{ValidTo: tstz(at), Kind: string(kind), Value: string(value)}); err != nil {
		return fmt.Errorf("close identity: %w", err)
	}
	return nil
}

func (t *pgTx) UpsertMember(ctx context.Context, m Member) error {
	var joined pgtype.Timestamptz
	if !m.JoinedAt.IsZero() {
		joined = tstz(m.JoinedAt)
	}
	err := t.q.UpsertMember(ctx, db.UpsertMemberParams{
		GroupJid: string(m.Group), PersonID: int64(m.Person), Role: string(m.Role), JoinedAt: joined,
		FirstSeen: m.FirstSeen, MsgCount: int32(m.MsgCount), CleanCount: int32(m.CleanCount),
		Allowlisted: m.Allowlisted, PreExisting: m.PreExisting, Established: m.Established,
	})
	if err != nil {
		return fmt.Errorf("upsert member: %w", err)
	}
	return nil
}

func (t *pgTx) DeleteMember(ctx context.Context, group wa.JID, id PersonID) error {
	if err := t.q.DeleteMember(ctx, db.DeleteMemberParams{GroupJid: string(group), PersonID: int64(id)}); err != nil {
		return fmt.Errorf("delete member: %w", err)
	}
	return nil
}

// Merge moves everything that references from to into. Memberships are combined like the memory
// store does; every other person column personReferences finds (identities and whatever later
// features add: strikes, detections, bans, corpus…) is re-pointed. A row that would then
// duplicate one the surviving person already has (a unique constraint) is dropped, since the
// survivor's row stands for the same fact.
func (t *pgTx) Merge(ctx context.Context, from, into PersonID, reason string, at time.Time) error {
	if _, err := t.Person(ctx, into); err != nil {
		return fmt.Errorf("merge into: %w", err)
	}
	fromMembers, err := t.Members(ctx, from)
	if err != nil {
		return err
	}
	for _, m := range fromMembers {
		if existing, ok, err := t.Member(ctx, m.Group, into); err != nil {
			return err
		} else if ok {
			m = mergeMember(existing, m)
		}
		m.Person = into
		if err := t.DeleteMember(ctx, m.Group, from); err != nil {
			return err
		}
		if err := t.UpsertMember(ctx, m); err != nil {
			return err
		}
	}

	refs, err := personReferences(ctx, t.tx)
	if err != nil {
		return fmt.Errorf("list person references: %w", err)
	}
	dropped := map[string]int{}
	for _, ref := range refs {
		// Excluded: members (combined above), persons.merged_into and person_merges (history that
		// must keep pointing at the old record). A new history column that has to survive a merge
		// must avoid the person_id / *_person / *_person_id names, or be listed here.
		if ref.SchemaName == "app" && (ref.TableName == "persons" || ref.TableName == "person_merges" || ref.TableName == "members") {
			continue
		}
		n, err := repoint(ctx, t.tx, ref.SchemaName, ref.TableName, ref.ColumnName, int64(from), int64(into))
		if err != nil {
			return fmt.Errorf("re-point %s.%s.%s: %w", ref.SchemaName, ref.TableName, ref.ColumnName, err)
		}
		if n > 0 {
			dropped[ref.TableName+"."+ref.ColumnName] += n
		}
	}

	if n, err := t.q.RetirePerson(ctx, db.RetirePersonParams{IntoPerson: pgtype.Int8{Int64: int64(into), Valid: true}, FromPerson: int64(from)}); err != nil {
		return fmt.Errorf("retire person %d: %w", from, err)
	} else if n == 0 {
		return fmt.Errorf("retire person %d: %w", from, ErrNoPerson)
	}
	if err := t.q.InsertPersonMerge(ctx, db.InsertPersonMergeParams{FromPerson: int64(from), IntoPerson: int64(into), Reason: reason, At: at}); err != nil {
		return fmt.Errorf("record merge: %w", err)
	}
	diff, err := json.Marshal(map[string]any{"from_person": from, "into_person": into, "reason": reason, "dropped_duplicates": dropped})
	if err != nil {
		return fmt.Errorf("encode merge audit: %w", err)
	}
	if _, err := store.WriteAudit(ctx, t.tx, store.AuditEntry{Actor: store.ActorSystem, Action: "identity.merge", Diff: diff}); err != nil {
		return fmt.Errorf("audit merge: %w", err)
	}
	return nil
}

type personRef struct{ SchemaName, TableName, ColumnName string }

// personReferences lists the columns that refer to a person: every single-column foreign key to
// app.persons(id), plus every bigint column in schema app named person_id, *_person or
// *_person_id (tables created before app.persons, like detections and group_bans, can't declare
// the key). A merge re-points them all, so tables later features add are covered without this
// package knowing them. (A catalog query, so it isn't in the sqlc query files.)
func personReferences(ctx context.Context, tx pgx.Tx) ([]personRef, error) {
	rows, err := tx.Query(ctx, `
SELECT n.nspname::text, c.relname::text, a.attname::text
FROM pg_constraint con
JOIN pg_class c ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = con.conkey[1]
WHERE con.contype = 'f' AND con.confrelid = 'app.persons'::regclass AND array_length(con.conkey, 1) = 1
UNION
SELECT c.table_schema::text, c.table_name::text, c.column_name::text
FROM information_schema.columns c
JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
WHERE c.table_schema = 'app' AND t.table_type = 'BASE TABLE' AND c.data_type = 'bigint'
  AND (c.column_name = 'person_id' OR c.column_name LIKE '%\_person' OR c.column_name LIKE '%\_person\_id')
ORDER BY 1, 2, 3`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (personRef, error) {
		var p personRef
		err := r.Scan(&p.SchemaName, &p.TableName, &p.ColumnName)
		return p, err
	})
}

const pgUniqueViolation = "23505"

// repoint sets col = into where col = from in one table, inside savepoints. If the bulk update
// hits a unique violation it goes row by row and deletes each row that conflicts. It returns how
// many rows were dropped as duplicates.
func repoint(ctx context.Context, tx pgx.Tx, schema, table, col string, from, into int64) (int, error) {
	tbl := pgx.Identifier{schema, table}.Sanitize()
	c := pgx.Identifier{col}.Sanitize()
	update := fmt.Sprintf("UPDATE %s SET %s = $1 WHERE %s = $2", tbl, c, c)
	err := savepoint(ctx, tx, "repoint", func() error {
		_, err := tx.Exec(ctx, update, into, from)
		return err
	})
	if err == nil || !isUnique(err) {
		return 0, err
	}
	rows, err := tx.Query(ctx, fmt.Sprintf("SELECT ctid::text FROM %s WHERE %s = $1", tbl, c), from)
	if err != nil {
		return 0, err
	}
	ctids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	dropped := 0
	for _, ctid := range ctids {
		err := savepoint(ctx, tx, "repoint_row", func() error {
			_, err := tx.Exec(ctx, fmt.Sprintf("UPDATE %s SET %s = $1 WHERE ctid = $2::tid", tbl, c), into, ctid)
			return err
		})
		if err == nil {
			continue
		}
		if !isUnique(err) {
			return dropped, err
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE ctid = $1::tid", tbl), ctid); err != nil {
			return dropped, err
		}
		dropped++
	}
	return dropped, nil
}

func savepoint(ctx context.Context, tx pgx.Tx, name string, fn func() error) error {
	if _, err := tx.Exec(ctx, "SAVEPOINT "+name); err != nil {
		return err
	}
	if err := fn(); err != nil {
		if _, rbErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+name); rbErr != nil {
			return errors.Join(err, rbErr)
		}
		return err
	}
	_, err := tx.Exec(ctx, "RELEASE SAVEPOINT "+name)
	return err
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func tstz(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
