//go:build integration

package policy_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/migrations"
)

// askDoc is a valid stored document with the given ask_before fallback; mode varies the rest of
// the document so "other keys untouched" is observable.
func askDoc(t *testing.T, on policy.TimeoutAction, mode policy.EnforcementMode) []byte {
	t.Helper()
	s := policy.Defaults()
	s.Enforcement.Mode = mode
	s.Enforcement.AskBefore.OnTimeout = on
	if err := s.Validate(); err != nil {
		t.Fatalf("setup: document with on_timeout=%s is invalid: %v", on, err)
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("setup: marshal: %v", err)
	}
	return raw
}

// TestAskBeforeKeep_Migration00038: documents still on the old delete_no_ban default (never
// another value in their history) switch to leave as a new version by "system"; groups that ever
// chose something else keep their value (maintainer decision 2026-10-18, DESIGN §12.1, §14).
func TestAskBeforeKeep_Migration00038(t *testing.T) {
	ctx := context.Background()
	pool, err := store.Open(ctx, storetest.URL(t, false))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(pool.Close)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.SQL,
		goose.WithGoMigrations(migrations.Go()...), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatalf("goose.NewProvider: %v", err)
	}
	if _, err := p.UpTo(ctx, 37); err != nil {
		t.Fatalf("migrate up to 00037: %v", err)
	}

	del, dis, lv := policy.TimeoutDeleteNoBan, policy.TimeoutDismiss, policy.TimeoutLeave
	auto, before := policy.EnforceAuto, policy.EnforceAskBeforeAction
	type version struct {
		on   policy.TimeoutAction
		mode policy.EnforcementMode
	}
	groups := []struct {
		jid       string
		history   []version // v1..vN; vN is current
		switched  bool
		rationale string
	}{
		{"g-default@g.us", []version{{del, auto}}, true, "never chose: still on the old default"},
		{"g-edited@g.us", []version{{del, auto}, {del, before}}, true, "other keys changed, ask_before.on_timeout never did"},
		{"g-chose-dismiss-then-back@g.us", []version{{del, auto}, {dis, before}, {del, before}}, false, "once chose dismiss, so delete_no_ban is a choice"},
		{"g-dismiss@g.us", []version{{dis, before}}, false, "current value is not delete_no_ban"},
		{"g-leave@g.us", []version{{lv, before}}, false, "already leave"},
	}
	docs := map[string][]byte{}
	for _, g := range groups {
		for i, v := range g.history {
			doc := askDoc(t, v.on, v.mode)
			if _, err := pool.Exec(ctx, `INSERT INTO app.group_settings_history (group_jid, version, doc, updated_by, updated_at) VALUES ($1, $2, $3, 'admin', now())`, g.jid, i+1, doc); err != nil {
				t.Fatalf("seed history %s v%d: %v", g.jid, i+1, err)
			}
			docs[g.jid] = doc
		}
		if _, err := pool.Exec(ctx, `INSERT INTO app.group_settings (group_jid, version, doc, updated_by) VALUES ($1, $2, $3, 'admin')`, g.jid, len(g.history), docs[g.jid]); err != nil {
			t.Fatalf("seed %s: %v", g.jid, err)
		}
	}

	res, err := store.Migrate(ctx, pool)
	if err != nil {
		t.Fatalf("store.Migrate after 00037: %v", err)
	}
	if res.To < 38 {
		t.Fatalf("schema version after Migrate = %d, want >= 38 (migration 00038 rewrites the old default)", res.To)
	}

	for _, g := range groups {
		t.Run(g.jid, func(t *testing.T) {
			var (
				ver int
				raw []byte
				by  string
			)
			if err := pool.QueryRow(ctx, `SELECT version, doc, updated_by FROM app.group_settings WHERE group_jid = $1`, g.jid).Scan(&ver, &raw, &by); err != nil {
				t.Fatalf("read %s: %v", g.jid, err)
			}
			got, err := policy.ParseStored(raw)
			if err != nil {
				t.Fatalf("ParseStored(migrated %s) error = %v, want a valid document", g.jid, err)
			}
			old, err := policy.ParseStored(docs[g.jid])
			if err != nil {
				t.Fatalf("ParseStored(seeded %s): %v", g.jid, err)
			}
			n := len(g.history)
			var histCount int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM app.group_settings_history WHERE group_jid = $1`, g.jid).Scan(&histCount); err != nil {
				t.Fatalf("count history: %v", err)
			}
			if !g.switched {
				if ver != n || got.Enforcement.AskBefore.OnTimeout != old.Enforcement.AskBefore.OnTimeout || histCount != n {
					t.Errorf("got version %d, on_timeout %q, %d history rows; want unchanged (version %d, %q, %d rows) (%s: a group's own choice is kept)",
						ver, got.Enforcement.AskBefore.OnTimeout, histCount, n, old.Enforcement.AskBefore.OnTimeout, n, g.rationale)
				}
				return
			}
			if got.Enforcement.AskBefore.OnTimeout != policy.TimeoutLeave {
				t.Errorf("on_timeout = %q, want leave (%s; maintainer decision 2026-10-18)", got.Enforcement.AskBefore.OnTimeout, g.rationale)
			}
			if ver != n+1 || by != store.ActorSystem {
				t.Errorf("got version %d by %q, want version %d by %q (the switch is a new version written by system)", ver, by, n+1, store.ActorSystem)
			}
			var hraw []byte
			var hby string
			if err := pool.QueryRow(ctx, `SELECT doc, updated_by FROM app.group_settings_history WHERE group_jid = $1 AND version = $2`, g.jid, n+1).Scan(&hraw, &hby); err != nil {
				t.Errorf("history row v%d for %s: %v, want one (every version is kept in history, DESIGN §14)", n+1, g.jid, err)
			} else if h, err := policy.ParseStored(hraw); err != nil || h.Enforcement.AskBefore.OnTimeout != policy.TimeoutLeave || hby != store.ActorSystem {
				t.Errorf("history v%d: on_timeout %q by %q (err %v), want leave by %q", n+1, h.Enforcement.AskBefore.OnTimeout, hby, err, store.ActorSystem)
			}
			got.Enforcement.AskBefore.OnTimeout = old.Enforcement.AskBefore.OnTimeout
			if !reflect.DeepEqual(got, old) {
				t.Errorf("migrated document differs from the seeded one in more than enforcement.ask_before.on_timeout, want every other key untouched")
			}
		})
	}
}
