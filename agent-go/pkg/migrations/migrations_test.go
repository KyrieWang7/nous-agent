//go:build docker

package migrations

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/dockertest/v3"
)

const latestVersion = 4

func TestUpMigratesFreshDatabaseAndIsIdempotent(t *testing.T) {
	databaseURL, db := startPostgres(t)
	dir := migrationDir(t)

	if err := Up(databaseURL, dir); err != nil {
		t.Fatal(err)
	}
	assertVersion(t, databaseURL, dir, latestVersion)

	if err := Up(databaseURL, dir); err != nil {
		t.Fatalf("repeating Up after the latest migration: %v", err)
	}
	assertVersion(t, databaseURL, dir, latestVersion)

	var exists bool
	if err := db.QueryRow(context.Background(), `SELECT to_regclass('public.agent_swarm_message_receipts') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("latest schema is missing agent_swarm_message_receipts")
	}
}

func TestSwarmReceiptMigrationBackfillsOnlyReadDirectMessages(t *testing.T) {
	databaseURL, db := startPostgres(t)
	dir := migrationDir(t)

	m, err := Open(databaseURL, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Steps(2); err != nil {
		closeMigrate(m)
		t.Fatal(err)
	}
	closeMigrate(m)
	assertVersion(t, databaseURL, dir, 2)

	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO agent_thread(id) VALUES ('lead-thread')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO agent_swarm_teams(id,lead_thread_id,name) VALUES ('team-1','lead-thread','test')`); err != nil {
		t.Fatal(err)
	}
	var directID, broadcastID int64
	if err := db.QueryRow(ctx, `INSERT INTO agent_swarm_messages(team_id,from_agent,to_agent,content,read_at) VALUES ('team-1','lead','bob','direct',NOW()) RETURNING id`).Scan(&directID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO agent_swarm_messages(team_id,from_agent,to_agent,content,read_at) VALUES ('team-1','lead','*','broadcast',NOW()) RETURNING id`).Scan(&broadcastID); err != nil {
		t.Fatal(err)
	}

	if err := Up(databaseURL, dir); err != nil {
		t.Fatal(err)
	}
	assertVersion(t, databaseURL, dir, latestVersion)
	assertReceiptCount(t, db, directID, 1)
	assertReceiptCount(t, db, broadcastID, 0)
}

func TestUpAcceptsLegacyPythonSwarmSchema(t *testing.T) {
	databaseURL, db := startPostgres(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `
CREATE TABLE swarm_teams (
    id UUID PRIMARY KEY,
    name VARCHAR(100) UNIQUE NOT NULL,
    description TEXT,
    lead_thread_id UUID NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE TABLE swarm_messages (
    id SERIAL PRIMARY KEY,
    team_id UUID NOT NULL REFERENCES swarm_teams(id) ON DELETE CASCADE,
    from_agent VARCHAR(100) NOT NULL,
    to_agent VARCHAR(100) NOT NULL,
    content TEXT NOT NULL,
    read BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW()
);`)
	if err != nil {
		t.Fatal(err)
	}

	if err := Up(databaseURL, migrationDir(t)); err != nil {
		t.Fatal(err)
	}

	var legacyReadAtExists bool
	if err := db.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'swarm_messages' AND column_name = 'read_at'
)`).Scan(&legacyReadAtExists); err != nil {
		t.Fatal(err)
	}
	if legacyReadAtExists {
		t.Fatal("Go migration modified the legacy Python swarm table")
	}

	var goTablesExist bool
	if err := db.QueryRow(ctx, `
SELECT to_regclass('public.agent_swarm_teams') IS NOT NULL
   AND to_regclass('public.agent_swarm_messages') IS NOT NULL
   AND to_regclass('public.agent_swarm_message_receipts') IS NOT NULL
`).Scan(&goTablesExist); err != nil {
		t.Fatal(err)
	}
	if !goTablesExist {
		t.Fatal("Go swarm tables were not created alongside the legacy Python schema")
	}
}

func startPostgres(t *testing.T) (string, *pgxpool.Pool) {
	t.Helper()
	pool, err := dockertest.NewPool("")
	if err != nil {
		t.Skipf("Docker unavailable: %v", err)
	}
	pool.MaxWait = 60 * time.Second
	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres",
		Tag:        "17-alpine",
		Env: []string{
			"POSTGRES_PASSWORD=postgres",
			"POSTGRES_USER=postgres",
			"POSTGRES_DB=postgres",
		},
	})
	if err != nil {
		t.Skipf("starting PostgreSQL container: %v", err)
	}
	_ = resource.Expire(120)
	t.Cleanup(func() {
		if err := pool.Purge(resource); err != nil {
			t.Logf("purging PostgreSQL container: %v", err)
		}
	})

	databaseURL := fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%s/postgres?sslmode=disable", resource.GetPort("5432/tcp"))
	var db *pgxpool.Pool
	if err := pool.Retry(func() error {
		candidate, err := pgxpool.New(context.Background(), databaseURL)
		if err != nil {
			return err
		}
		if err := candidate.Ping(context.Background()); err != nil {
			candidate.Close()
			return err
		}
		db = candidate
		return nil
	}); err != nil {
		t.Fatalf("connecting to PostgreSQL: %v", err)
	}
	t.Cleanup(db.Close)
	return databaseURL, db
}

func migrationDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func assertVersion(t *testing.T, databaseURL, dir string, want uint) {
	t.Helper()
	version, dirty, err := Version(databaseURL, dir)
	if err != nil {
		t.Fatal(err)
	}
	if version != want || dirty {
		t.Fatalf("migration state = version %d, dirty %t; want version %d, dirty false", version, dirty, want)
	}
}

func assertReceiptCount(t *testing.T, db *pgxpool.Pool, messageID int64, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow(context.Background(), `SELECT COUNT(*) FROM agent_swarm_message_receipts WHERE message_id=$1`, messageID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("receipt count for message %d = %d, want %d", messageID, count, want)
	}
}
