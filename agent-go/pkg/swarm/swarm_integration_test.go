//go:build docker

package swarm

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/dockertest/v3"
)

func TestPostgresCapacityAndBroadcastDelivery(t *testing.T) {
	databaseURL, pool := startSwarmPostgres(t)
	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(databaseURL, migrationDir); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	for _, threadID := range []string{"capacity-lead", "broadcast-lead"} {
		if _, err := pool.Exec(ctx, `INSERT INTO agent_thread(id) VALUES($1)`, threadID); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("serializes capacity across concurrent registrations", func(t *testing.T) {
		manager, err := New(pool, Options{MaxTeamSize: 2})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.CreateTeam(ctx, "capacity-team", "capacity", "capacity-lead"); err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		errorsByMember := make(chan error, 2)
		var workers sync.WaitGroup
		for _, name := range []string{"alice", "bob"} {
			name := name
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				errorsByMember <- manager.RegisterMember(ctx, Member{
					TeamID: "capacity-team", Name: name, Status: MemberStatusRunning,
				}, "[Joined] "+name+" started working on: capacity test")
			}()
		}
		close(start)
		workers.Wait()
		close(errorsByMember)

		succeeded, full := 0, 0
		for err := range errorsByMember {
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrTeamFull):
				full++
			default:
				t.Fatalf("unexpected registration error: %v", err)
			}
		}
		if succeeded != 1 || full != 1 {
			t.Fatalf("registration outcomes: succeeded=%d full=%d", succeeded, full)
		}
	})

	t.Run("delivers one broadcast once to every existing member", func(t *testing.T) {
		manager, err := New(pool, Options{MaxTeamSize: 3})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.CreateTeam(ctx, "broadcast-team", "broadcast", "broadcast-lead"); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"alice", "bob"} {
			if err := manager.RegisterMember(ctx, Member{
				TeamID: "broadcast-team", Name: name, Status: MemberStatusRunning,
			}, "[Joined] "+name+" started working on: broadcast test"); err != nil {
				t.Fatal(err)
			}
		}
		if err := manager.Send(ctx, "broadcast-team", LeadAgentName, "*", "shared update"); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"alice", "bob"} {
			messages, err := manager.Poll(ctx, "broadcast-team", name, 50)
			if err != nil {
				t.Fatal(err)
			}
			if countContent(messages, "shared update") != 1 {
				t.Fatalf("%s messages = %#v", name, messages)
			}
			again, err := manager.Poll(ctx, "broadcast-team", name, 50)
			if err != nil {
				t.Fatal(err)
			}
			if countContent(again, "shared update") != 0 {
				t.Fatalf("%s received the broadcast twice: %#v", name, again)
			}
		}
	})
}

func countContent(messages []Message, content string) int {
	count := 0
	for _, message := range messages {
		if message.Content == content {
			count++
		}
	}
	return count
}

func startSwarmPostgres(t *testing.T) (string, *pgxpool.Pool) {
	t.Helper()
	dockerPool, err := dockertest.NewPool("")
	if err != nil {
		t.Skipf("Docker unavailable: %v", err)
	}
	dockerPool.MaxWait = 60 * time.Second
	resource, err := dockerPool.RunWithOptions(&dockertest.RunOptions{
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
		if err := dockerPool.Purge(resource); err != nil {
			t.Logf("purging PostgreSQL container: %v", err)
		}
	})

	databaseURL := fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%s/postgres?sslmode=disable", resource.GetPort("5432/tcp"))
	var pool *pgxpool.Pool
	if err := dockerPool.Retry(func() error {
		candidate, err := pgxpool.New(context.Background(), databaseURL)
		if err != nil {
			return err
		}
		if err := candidate.Ping(context.Background()); err != nil {
			candidate.Close()
			return err
		}
		pool = candidate
		return nil
	}); err != nil {
		t.Fatalf("connecting to PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	return databaseURL, pool
}
