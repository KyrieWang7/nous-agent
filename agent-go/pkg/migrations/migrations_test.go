//go:build docker

package migrations

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/memory"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	agentruntime "github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/metadata"
	runtimepostgres "github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/postgres"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/replay"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/dockertest/v3"
)

const latestVersion = 12

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
	var lifecycleColumns bool
	if err := db.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name='agent_swarm_teams' AND column_name='description'
		) AND EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name='agent_swarm_team_members' AND column_name='joined_at'
		)`).Scan(&lifecycleColumns); err != nil {
		t.Fatal(err)
	}
	if !lifecycleColumns {
		t.Fatal("latest schema is missing swarm lifecycle columns")
	}
	var eventIdempotency bool
	if err := db.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name='agent_run_event' AND column_name='idempotency_key'
		)`).Scan(&eventIdempotency); err != nil {
		t.Fatal(err)
	}
	if !eventIdempotency {
		t.Fatal("latest schema is missing event idempotency key")
	}
	var approvalTable bool
	if err := db.QueryRow(context.Background(), `SELECT to_regclass('public.agent_approval') IS NOT NULL`).Scan(&approvalTable); err != nil {
		t.Fatal(err)
	}
	if !approvalTable {
		t.Fatal("latest schema is missing agent_approval")
	}
	var canonicalUsageColumn, removedUsageColumn bool
	if err := db.QueryRow(context.Background(), `
SELECT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name='agent_run_completion' AND column_name='auxiliary_tokens'
), EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name='agent_run_completion' AND column_name='middleware_tokens'
)`).Scan(&canonicalUsageColumn, &removedUsageColumn); err != nil {
		t.Fatal(err)
	}
	if !canonicalUsageColumn || removedUsageColumn {
		t.Fatalf("usage columns = auxiliary:%t middleware:%t, want true/false", canonicalUsageColumn, removedUsageColumn)
	}
	var scopedMemory bool
	if err := db.QueryRow(context.Background(), `
		SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='memory_fact' AND column_name='user_id')
		AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='memory_fact' AND column_name='project_id')
		AND to_regclass('public.memory_fact_scope_unique_idx') IS NOT NULL`).Scan(&scopedMemory); err != nil {
		t.Fatal(err)
	}
	if !scopedMemory {
		t.Fatal("latest schema is missing scoped memory columns or unique index")
	}
	var snapshotTable bool
	if err := db.QueryRow(context.Background(), `SELECT to_regclass('public.agent_projection_snapshot') IS NOT NULL`).Scan(&snapshotTable); err != nil {
		t.Fatal(err)
	}
	if !snapshotTable {
		t.Fatal("latest schema is missing agent_projection_snapshot")
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

func TestGoMigrationsIgnoreUnownedSwarmTables(t *testing.T) {
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

	var unownedReadAtExists bool
	if err := db.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'swarm_messages' AND column_name = 'read_at'
)`).Scan(&unownedReadAtExists); err != nil {
		t.Fatal(err)
	}
	if unownedReadAtExists {
		t.Fatal("Go migration modified an unowned swarm table")
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
		t.Fatal("Go-owned swarm tables were not created in their agent_ namespace")
	}
}

func TestPostgresApprovalFirstTerminalDecisionWinsAcrossManagers(t *testing.T) {
	databaseURL, db := startPostgres(t)
	if err := Up(databaseURL, migrationDir(t)); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO agent_thread(id) VALUES ('thread-approval-race')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO agent_run(id,thread_id,status) VALUES ('run-approval-race','thread-approval-race','running')`); err != nil {
		t.Fatal(err)
	}

	store, err := runtimepostgres.NewApprovalStore(db)
	if err != nil {
		t.Fatal(err)
	}
	creator, err := agentruntime.NewApprovalManager(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := creator.Create(ctx, agentruntime.ApprovalRequest{
		ID:            "approval-race",
		RunID:         "run-approval-race",
		TransactionID: "transaction-race",
		ToolName:      "write",
	}); err != nil {
		t.Fatal(err)
	}

	// Each manager has its own mutex. Holding both first reads at pending forces
	// the opposing writes to race at PostgreSQL's conditional upsert.
	barrier := &approvalReadBarrier{
		ApprovalStore: store,
		release:       make(chan struct{}),
	}
	approver, err := agentruntime.NewApprovalManager(barrier)
	if err != nil {
		t.Fatal(err)
	}
	rejecter, err := agentruntime.NewApprovalManager(barrier)
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		request agentruntime.ApprovalRequest
		err     error
	}
	results := make(chan result, 2)
	go func() {
		req, decideErr := approver.Decide(ctx, "approval-race", true, "approver")
		results <- result{request: req, err: decideErr}
	}()
	go func() {
		req, decideErr := rejecter.Decide(ctx, "approval-race", false, "rejecter")
		results <- result{request: req, err: decideErr}
	}()

	var decisions [2]agentruntime.ApprovalRequest
	for i := range decisions {
		select {
		case got := <-results:
			if got.err != nil {
				t.Fatalf("Decide() error = %v", got.err)
			}
			decisions[i] = got.request
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent approval decisions did not finish")
		}
	}

	final, err := store.Get(ctx, "approval-race")
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != agentruntime.ApprovalApproved && final.Status != agentruntime.ApprovalRejected {
		t.Fatalf("final status = %q, want approved or rejected", final.Status)
	}
	for i, decision := range decisions {
		if decision.Status != final.Status || decision.DecisionBy != final.DecisionBy {
			t.Errorf("decision %d = (%q, %q), final = (%q, %q)",
				i, decision.Status, decision.DecisionBy, final.Status, final.DecisionBy)
		}
	}
}

func TestPostgresQuestionFirstTerminalAnswerWinsAcrossManagers(t *testing.T) {
	databaseURL, db := startPostgres(t)
	if err := Up(databaseURL, migrationDir(t)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO agent_thread(id) VALUES ('thread-question-race')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO agent_run(id,thread_id,status) VALUES ('run-question-race','thread-question-race','running')`); err != nil {
		t.Fatal(err)
	}
	store, err := runtimepostgres.NewQuestionStore(db)
	if err != nil {
		t.Fatal(err)
	}
	creator, err := agentruntime.NewQuestionManager(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := creator.Create(ctx, agentruntime.QuestionRequest{
		ID: "question-race", RunID: "run-question-race", ThreadID: "thread-question-race",
		Header: "Plan review", Question: "Approve?",
		Options: []agentruntime.QuestionOption{{Label: "Approve"}, {Label: "Keep planning"}},
	}); err != nil {
		t.Fatal(err)
	}
	barrier := &questionReadBarrier{QuestionStore: store, release: make(chan struct{})}
	approver, err := agentruntime.NewQuestionManager(barrier)
	if err != nil {
		t.Fatal(err)
	}
	planner, err := agentruntime.NewQuestionManager(barrier)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		request agentruntime.QuestionRequest
		err     error
	}
	results := make(chan result, 2)
	go func() {
		request, answerErr := approver.Answer(ctx, "question-race", agentruntime.QuestionAnswer{Selected: []string{"Approve"}}, "approver")
		results <- result{request: request, err: answerErr}
	}()
	go func() {
		request, answerErr := planner.Answer(ctx, "question-race", agentruntime.QuestionAnswer{Selected: []string{"Keep planning"}}, "planner")
		results <- result{request: request, err: answerErr}
	}()
	var answers [2]agentruntime.QuestionRequest
	for i := range answers {
		select {
		case got := <-results:
			if got.err != nil {
				t.Fatal(got.err)
			}
			answers[i] = got.request
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent question answers did not finish")
		}
	}
	final, err := store.Get(ctx, "question-race")
	if err != nil {
		t.Fatal(err)
	}
	for i, answer := range answers {
		if answer.AnsweredBy != final.AnsweredBy || answer.Answer.Selected[0] != final.Answer.Selected[0] {
			t.Errorf("answer %d=%#v final=%#v", i, answer, final)
		}
	}
}

func TestPostgresProjectionSnapshotRoundTripAndMonotonicCursor(t *testing.T) {
	databaseURL, db := startPostgres(t)
	if err := Up(databaseURL, migrationDir(t)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO agent_thread(id) VALUES ('thread-snapshot')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO agent_run(id,thread_id,status) VALUES ('run-snapshot','thread-snapshot','running')`); err != nil {
		t.Fatal(err)
	}
	store, err := runtimepostgres.NewSnapshotStore(db)
	if err != nil {
		t.Fatal(err)
	}
	newer := replay.Snapshot{RunID: "run-snapshot", ThreadID: "thread-snapshot", LastSeq: 8, Messages: []message.Message{{Role: message.RoleAssistant, Content: "new"}}, CreatedAt: time.Now().UTC()}
	if err := store.Save(ctx, newer); err != nil {
		t.Fatal(err)
	}
	older := newer
	older.LastSeq = 3
	older.Messages[0].Content = "stale"
	if err := store.Save(ctx, older); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(ctx, newer.RunID, newer.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSeq != 8 || len(got.Messages) != 1 || got.Messages[0].Content != "new" {
		t.Fatalf("snapshot = %#v", got)
	}
	if _, err := db.Exec(ctx, `DELETE FROM agent_projection_snapshot WHERE run_id='run-snapshot'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ctx, newer.RunID, newer.ThreadID); !errors.Is(err, replay.ErrSnapshotNotFound) {
		t.Fatalf("Load() error = %v, want ErrSnapshotNotFound", err)
	}
}

func TestPostgresRuntimeMetadataStoreOwnsExecutionState(t *testing.T) {
	databaseURL, db := startPostgres(t)
	if err := Up(databaseURL, migrationDir(t)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO agent_thread(id,state) VALUES ('thread-runtime-metadata','{"seed":true}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO agent_run(id,thread_id,status) VALUES ('run-runtime-metadata','thread-runtime-metadata','pending')`); err != nil {
		t.Fatal(err)
	}
	store, err := runtimepostgres.NewMetadataStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHistory(ctx, "thread-runtime-metadata", []message.Message{{Role: message.RoleUser, Content: "question"}}, false); err != nil {
		t.Fatal(err)
	}
	history, err := store.LoadHistory(ctx, "thread-runtime-metadata")
	if err != nil || len(history) != 1 || history[0].Content != "question" {
		t.Fatalf("history = %#v, err = %v", history, err)
	}
	if err := store.SaveThreadValues(ctx, "thread-runtime-metadata", map[string]any{"answer": 42, "title": "Runtime"}); err != nil {
		t.Fatal(err)
	}
	values, err := store.LoadThreadValues(ctx, "thread-runtime-metadata")
	if err != nil || values["seed"] != true || values["answer"] != float64(42) || values["title"] != "Runtime" {
		t.Fatalf("values = %#v, err = %v", values, err)
	}
	if err := store.MarkRunRunning(ctx, "run-runtime-metadata"); err != nil {
		t.Fatal(err)
	}
	first, err := store.ConfirmRunTerminal(ctx, "run-runtime-metadata", metadata.TerminalState{Status: "success", RiskLevel: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.ConfirmRunTerminal(ctx, "run-runtime-metadata", metadata.TerminalState{Status: "error", RiskLevel: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "success" || second != first {
		t.Fatalf("terminal states = %#v then %#v", first, second)
	}
	completion := metadata.Completion{RunID: "run-runtime-metadata", ThreadID: "thread-runtime-metadata", Status: "success", LLMCalls: 1, InputTokens: 10, CompletedAt: time.Now().UTC()}
	if err := store.SaveCompletion(ctx, completion); err != nil {
		t.Fatal(err)
	}
	var status string
	var calls, input int
	if err := db.QueryRow(ctx, `SELECT status,llm_call_count,input_tokens FROM agent_run_completion WHERE run_id='run-runtime-metadata'`).Scan(&status, &calls, &input); err != nil {
		t.Fatal(err)
	}
	if status != "success" || calls != 1 || input != 10 {
		t.Fatalf("completion = status:%s calls:%d input:%d", status, calls, input)
	}
}

func TestPostgresMemoryStorePreservesCapabilityScope(t *testing.T) {
	databaseURL, db := startPostgres(t)
	if err := Up(databaseURL, migrationDir(t)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO agent_thread(id) VALUES ('thread-memory-store')`); err != nil {
		t.Fatal(err)
	}
	store, err := runtimepostgres.NewMemoryStore(db)
	if err != nil {
		t.Fatal(err)
	}
	scope := memory.Scope{UserID: "user-1", ProjectID: "project-1", ThreadID: "thread-memory-store"}
	if err := store.Add(ctx, memory.Fact{UserID: scope.UserID, ProjectID: scope.ProjectID, ThreadID: scope.ThreadID, Text: "prefers Go", Confidence: 0.8}); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(ctx, memory.Fact{UserID: scope.UserID, ProjectID: scope.ProjectID, ThreadID: scope.ThreadID, Text: "prefers Go", Confidence: 0.9}); err != nil {
		t.Fatal(err)
	}
	facts, err := store.List(ctx, scope, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Text != "prefers Go" || facts[0].Confidence != 0.9 {
		t.Fatalf("facts = %#v", facts)
	}
	other, err := store.List(ctx, memory.Scope{UserID: "user-2", ProjectID: scope.ProjectID, ThreadID: scope.ThreadID}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("cross-scope facts = %#v", other)
	}
}

type approvalReadBarrier struct {
	agentruntime.ApprovalStore
	reads   atomic.Int32
	release chan struct{}
}

type questionReadBarrier struct {
	agentruntime.QuestionStore
	reads   atomic.Int32
	release chan struct{}
}

func (s *questionReadBarrier) Get(ctx context.Context, id string) (agentruntime.QuestionRequest, error) {
	request, err := s.QuestionStore.Get(ctx, id)
	if err != nil {
		return request, err
	}
	if read := s.reads.Add(1); read <= 2 {
		if read == 2 {
			close(s.release)
		}
		select {
		case <-s.release:
		case <-ctx.Done():
			return agentruntime.QuestionRequest{}, ctx.Err()
		}
	}
	return request, nil
}

func (s *approvalReadBarrier) Get(ctx context.Context, id string) (agentruntime.ApprovalRequest, error) {
	req, err := s.ApprovalStore.Get(ctx, id)
	if err != nil {
		return req, err
	}
	if read := s.reads.Add(1); read <= 2 {
		if read == 2 {
			close(s.release)
		}
		select {
		case <-s.release:
		case <-ctx.Done():
			return agentruntime.ApprovalRequest{}, ctx.Err()
		}
	}
	return req, nil
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
