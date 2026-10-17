//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/migrations"
)

func TestOpenRunsInUTC(t *testing.T) {
	// Pretend the host is elsewhere: scanned times must still come back in UTC (SQL-004).
	riyadh, err := time.LoadLocation("Asia/Riyadh")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	old := time.Local
	time.Local = riyadh
	t.Cleanup(func() { time.Local = old })

	pool := storetest.Pool(t)
	ctx := context.Background()
	var tz string
	var at time.Time
	if err := pool.QueryRow(ctx, "SELECT current_setting('TimeZone'), '2026-01-02 03:04:05+00'::timestamptz").Scan(&tz, &at); err != nil {
		t.Fatal(err)
	}
	if tz != "UTC" {
		t.Errorf("session TimeZone %q, want UTC", tz)
	}
	if at.Location() != time.UTC {
		t.Errorf("timestamptz scanned in %v, want UTC (times must not depend on the host TZ)", at.Location())
	}
}

func TestInTx(t *testing.T) {
	pool := storetest.Pool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "CREATE TABLE app.t (v int)"); err != nil {
		t.Fatal(err)
	}
	insert := func(tx pgx.Tx) error { _, err := tx.Exec(ctx, "INSERT INTO app.t VALUES (1)"); return err }
	errBoom := errors.New("boom")

	tests := []struct {
		name      string
		fn        func(pgx.Tx) error
		wantErr   error
		wantPanic bool
		wantRows  int
	}{
		{"commits on nil", insert, nil, false, 1},
		{"rolls back on error", func(tx pgx.Tx) error { _ = insert(tx); return errBoom }, errBoom, false, 0},
		{"rolls back on panic", func(tx pgx.Tx) error { _ = insert(tx); panic("bad") }, nil, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, "TRUNCATE app.t"); err != nil {
				t.Fatal(err)
			}
			func() {
				defer func() {
					if r := recover(); (r != nil) != tt.wantPanic {
						t.Errorf("panic = %v, want panic %v", r, tt.wantPanic)
					}
				}()
				if err := store.InTx(ctx, pool, tt.fn); !errors.Is(err, tt.wantErr) {
					t.Errorf("InTx error %v, want %v", err, tt.wantErr)
				}
			}()
			var n int
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM app.t").Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != tt.wantRows {
				t.Errorf("%d rows after InTx, want %d (a failed transaction must leave nothing behind)", n, tt.wantRows)
			}
		})
	}
}

func TestMigrateFreshDatabase(t *testing.T) {
	ctx := context.Background()
	pool, err := store.Open(ctx, storetest.URL(t, false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	newest, err := store.NewestMigration()
	if err != nil {
		t.Fatal(err)
	}

	res, err := store.Migrate(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if res.From != 0 || res.To != newest || res.Applied != int(newest) {
		t.Errorf("first Migrate = %+v, want 0 → %d with %d applied", res, newest, newest)
	}
	for _, rel := range []string{"app.audit_log", "app.river_job", "app.river_migration", "app.river_leader"} {
		var ok bool
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", rel).Scan(&ok); err != nil || !ok {
			t.Errorf("table %s missing after Migrate (err %v)", rel, err)
		}
	}
	var schemas int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM pg_namespace WHERE nspname IN ('app', 'whatsmeow')").Scan(&schemas); err != nil || schemas != 2 {
		t.Errorf("found %d of schemas app and whatsmeow (err %v), want both", schemas, err)
	}
	var riverVersion int
	if err := pool.QueryRow(ctx, "SELECT max(version) FROM app.river_migration").Scan(&riverVersion); err != nil || riverVersion != migrations.RiverSchemaVersion {
		t.Errorf("River schema at %d (err %v), want %d", riverVersion, err, migrations.RiverSchemaVersion)
	}

	again, err := store.Migrate(ctx, pool)
	if err != nil || again.Applied != 0 || again.To != newest {
		t.Errorf("second Migrate = %+v, %v; want nothing applied (migrations run on every start)", again, err)
	}
}

func TestMigrateRefusesNewerSchema(t *testing.T) {
	pool := storetest.Pool(t)
	ctx := context.Background()
	newest, err := store.NewestMigration()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)", newest+1); err != nil {
		t.Fatal(err)
	}
	_, err = store.Migrate(ctx, pool)
	var tooNew *store.SchemaTooNewError
	if !errors.As(err, &tooNew) || !errors.Is(err, store.ErrSchemaTooNew) {
		t.Fatalf("Migrate on a newer schema: %v, want SchemaTooNewError (running on an unknown schema can corrupt data)", err)
	}
	if tooNew.DBVersion != newest+1 || tooNew.BinaryVersion != newest {
		t.Errorf("got %+v, want db %d binary %d", tooNew, newest+1, newest)
	}
}

func TestAuditLog(t *testing.T) {
	pool := storetest.Pool(t)
	ctx := context.Background()
	const group = "120363000000000001@g.us"

	diff := json.RawMessage(`{"mode":{"from":"standard","to":"strict"}}`)
	first, err := store.WriteAudit(ctx, pool, store.AuditEntry{Actor: "966512345612", GroupJID: group, Action: "settings.update", Diff: diff})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == 0 || first.At.IsZero() {
		t.Errorf("stored entry %+v lacks ID or time", first)
	}
	if _, err := store.WriteAudit(ctx, pool, store.AuditEntry{Actor: store.ActorSystem, GroupJID: group, Action: "action.ban"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteAudit(ctx, pool, store.AuditEntry{Actor: store.ActorSystem, GroupJID: "other@g.us", Action: "action.delete"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteAudit(ctx, pool, store.AuditEntry{Actor: store.ActorOperator, Action: "instance.pacing"}); err != nil {
		t.Fatal(err)
	}
	// An entry written in a rolled-back transaction disappears with the change it records.
	_ = store.InTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := store.WriteAudit(ctx, tx, store.AuditEntry{Actor: store.ActorSystem, GroupJID: group, Action: "rolled.back"}); err != nil {
			t.Error(err)
		}
		return errors.New("abort")
	})

	got, err := store.ListAudit(ctx, pool, group, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Action != "action.ban" || got[1].Action != "settings.update" {
		t.Fatalf("group log = %+v, want [action.ban settings.update] newest first and only this group's", got)
	}
	if string(got[0].Diff) != "{}" {
		t.Errorf("nil diff stored as %s, want {}", got[0].Diff)
	}
	var gotDiff, wantDiff any
	_ = json.Unmarshal(got[1].Diff, &gotDiff)
	_ = json.Unmarshal(diff, &wantDiff)
	if got[1].Actor != "966512345612" || got[1].GroupJID != group || !jsonEqual(gotDiff, wantDiff) {
		t.Errorf("entry %+v does not round-trip what was written", got[1])
	}

	page2, err := store.ListAudit(ctx, pool, group, got[0].ID, 10)
	if err != nil || len(page2) != 1 || page2[0].ID != got[1].ID {
		t.Errorf("page after %d = %+v, %v; want only the older entry", got[0].ID, page2, err)
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
