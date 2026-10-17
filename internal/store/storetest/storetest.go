//go:build integration

package storetest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
)

// Image is the Postgres image the tests run against (the one docker-compose.test.yml uses).
const Image = "postgres:18-alpine"

const templateDB = "antispam_template"

var (
	startOnce sync.Once
	adminURL  string // superuser URL for the container's maintenance database
	startErr  error
	dbSeq     atomic.Int64
)

// start runs one container for the whole test binary, plus a migrated template database that
// each Pool clones (CREATE DATABASE … TEMPLATE is much faster than migrating every time).
// Ryuk removes the container when the test process exits.
func start() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c, err := postgres.Run(ctx, Image,
		postgres.WithDatabase("postgres"),
		postgres.WithUsername("antispam"),
		postgres.WithPassword("antispam"),
		testcontainers.WithEnv(map[string]string{"TZ": "UTC", "PGTZ": "UTC"}),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		startErr = fmt.Errorf("start postgres container: %w", err)
		return
	}
	if adminURL, err = c.ConnectionString(ctx, "sslmode=disable"); err != nil {
		startErr = fmt.Errorf("postgres connection string: %w", err)
		return
	}
	if err := exec(ctx, "CREATE DATABASE "+templateDB); err != nil {
		startErr = err
		return
	}
	pool, err := store.Open(ctx, withDB(adminURL, templateDB))
	if err != nil {
		startErr = err
		return
	}
	defer pool.Close()
	if _, err := store.Migrate(ctx, pool); err != nil {
		startErr = fmt.Errorf("migrate template: %w", err)
	}
}

// URL creates a fresh database for t and returns its connection URL. With migrated set, the
// database already has every embedded migration applied; otherwise it's empty. The database is
// dropped when t finishes.
func URL(t testing.TB, migrated bool) string {
	t.Helper()
	startOnce.Do(start)
	if startErr != nil {
		t.Fatalf("storetest: %v (integration tests need Docker running)", startErr)
	}
	name := fmt.Sprintf("t_%d_%d", os.Getpid(), dbSeq.Add(1))
	stmt := "CREATE DATABASE " + name
	if migrated {
		stmt += " TEMPLATE " + templateDB
	}
	ctx := context.Background()
	if err := exec(ctx, stmt); err != nil {
		t.Fatalf("storetest: %v", err)
	}
	t.Cleanup(func() {
		if err := exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Logf("storetest: drop %s: %v", name, err)
		}
	})
	return withDB(adminURL, name)
}

// Pool returns a pool on a fresh, fully migrated database, closed when t finishes.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool, err := store.Open(context.Background(), URL(t, true))
	if err != nil {
		t.Fatalf("storetest: open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func exec(ctx context.Context, sql string) error {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if _, err := conn.Exec(ctx, sql); err != nil {
		return fmt.Errorf("%s: %w", strings.Fields(sql)[0]+" "+strings.Fields(sql)[1], err)
	}
	return nil
}

func withDB(raw, name string) string {
	u, err := url.Parse(raw)
	if err != nil {
		panic(fmt.Sprintf("storetest: bad container URL: %v", err))
	}
	u.Path = "/" + name
	return u.String()
}
