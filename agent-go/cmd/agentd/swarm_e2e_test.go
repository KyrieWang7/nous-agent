//go:build docker

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/transport/httpapi"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/config"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/migrations"
	runtimepostgres "github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/dockertest/v3"
)

func TestSwarmTaskHTTPFlowPersistsLifecycleAndUsage(t *testing.T) {
	databaseURL, pool := startAgentdPostgres(t)
	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(databaseURL, migrationDir); err != nil {
		t.Fatal(err)
	}

	var providerCalls atomic.Int32
	var requestMu sync.Mutex
	var requestKinds []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawRequest, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			http.Error(w, readErr.Error(), http.StatusBadRequest)
			return
		}
		kind := "lead"
		if strings.Contains(string(rawRequest), "<subagent_profile>") {
			kind = "subagent"
		}
		requestMu.Lock()
		requestKinds = append(requestKinds, kind)
		requestMu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		var err error
		switch providerCalls.Add(1) {
		case 1:
			err = writeToolCallStream(w, "provider-plan-todos", "plan-todos", "write_todos", map[string]any{
				"todos": []map[string]any{{"content": "Coordinate the research team", "status": "pending"}},
			}, 10, 2)
		case 2:
			err = writeToolCallStream(w, "provider-plan-review", "plan-review", "exit_plan_mode", map[string]any{
				"plan": "# Swarm execution plan\n\nCreate the team, delegate the research task, then synthesize the result.",
			}, 12, 2)
		case 3:
			err = writeToolCallStream(w, "provider-team", "team-create", "team_create", map[string]any{
				"name": "e2e-team", "description": "end-to-end team",
			}, 12, 2)
		case 4:
			err = writeToolCallStream(w, "provider-task", "task-1", "task", map[string]any{
				"description":   "Research evidence",
				"prompt":        "Return the verified evidence",
				"subagent_type": "explore",
				"name":          "researcher",
			}, 18, 3)
		case 5:
			err = writeTextStream(w, "provider-child", "verified evidence", 7, 4)
		case 6:
			err = writeToolCallStream(w, "provider-complete-todos", "complete-todos", "write_todos", map[string]any{
				"todos": []map[string]any{{"content": "Coordinate the research team", "status": "completed"}},
			}, 10, 2)
		case 7:
			err = writeTextStream(w, "provider-final", "final synthesis", 20, 5)
		default:
			http.Error(w, "unexpected provider call", http.StatusInternalServerError)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	t.Cleanup(provider.Close)

	cfg := config.Defaults()
	cfg.Models = []config.ModelConfig{{
		Name: "stub", Provider: "openai-compatible", Model: "stub",
		BaseURL: provider.URL, ContextLength: 10_000,
	}}
	cfg.DefaultModel = "stub"
	cfg.Runtime.DatabaseURL = databaseURL
	cfg.Sandbox.Enabled = false
	cfg.Subagents.Enabled = true
	cfg.Swarm.Enabled = true
	cfg.Title.Enabled = false
	cfg.Memory.Enabled = false
	cfg.Summarization.Enabled = false
	cfg.Guardrails.Enabled = false

	built, err := buildAgent(cfg, nil, pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(built.Close)
	postgresStore, err := httpapi.NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	postgresEvents, err := runtimepostgres.NewEventStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	api, err := httpapi.New(httpapi.Options{
		Agent: built.agent, Store: postgresStore, EventStore: postgresEvents,
		AllowedTools: built.tools, Pricer: buildPricer(cfg.Models),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Close)
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)

	postJSON(t, server.URL+"/api/v1/threads", `{"thread_id":"swarm-e2e-thread"}`)
	response, err := http.Post(
		server.URL+"/api/v1/threads/swarm-e2e-thread/runs",
		"application/json",
		strings.NewReader(`{
			"input":{"messages":[{"type":"human","content":"coordinate the work"}]},
			"context":{"mode":"ultra","swarm_enabled":true},
			"on_disconnect":"continue"
		}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("run status = %s", response.Status)
	}

	customTypes := map[string]int{}
	customPayloads := map[string][]map[string]any{}
	var finalText strings.Builder
	var runErrors []string
	planApproved := false
	scanner := bufio.NewScanner(response.Body)
	currentEvent := ""
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			raw := []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			switch currentEvent {
			case "custom":
				var payload map[string]any
				if err := json.Unmarshal(raw, &payload); err != nil {
					t.Fatal(err)
				}
				if typ, _ := payload["type"].(string); typ != "" {
					customTypes[typ]++
					customPayloads[typ] = append(customPayloads[typ], payload)
					if typ == "question_requested" {
						approvePlanReview(t, server.URL, payload)
						planApproved = true
					}
				}
			case "messages":
				var payload []map[string]any
				if err := json.Unmarshal(raw, &payload); err != nil {
					t.Fatal(err)
				}
				if len(payload) > 0 {
					if content, ok := payload[0]["content"].(string); ok {
						finalText.WriteString(content)
					}
				}
			case "error":
				runErrors = append(runErrors, string(raw))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if providerCalls.Load() != 7 {
		t.Fatalf("provider calls = %d (%v), custom events=%v, task failures=%v, errors=%v, final=%q", providerCalls.Load(), requestKinds, customTypes, customPayloads["task_failed"], runErrors, finalText.String())
	}
	if !planApproved {
		t.Fatal("plan review was never requested or approved")
	}
	for _, typ := range []string{"question_requested", "question_resolved", "task_started", "task_running", "task_completed", "token_usage"} {
		if customTypes[typ] == 0 {
			t.Fatalf("missing %s event; custom events=%v", typ, customTypes)
		}
	}
	if got := finalText.String(); got != "final synthesis" {
		t.Fatalf("final streamed text = %q", got)
	}

	ctx := context.Background()
	var memberStatus string
	if err := pool.QueryRow(ctx, `
		SELECT m.status
		FROM agent_swarm_team_members m
		JOIN agent_swarm_teams t ON t.id=m.team_id
		WHERE t.lead_thread_id=$1 AND m.name=$2`, "swarm-e2e-thread", "researcher").Scan(&memberStatus); err != nil {
		t.Fatal(err)
	}
	if memberStatus != "completed" {
		t.Fatalf("member status = %q", memberStatus)
	}
	var completedAnnouncements int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM agent_swarm_messages m
		JOIN agent_swarm_teams t ON t.id=m.team_id
		WHERE t.lead_thread_id=$1 AND m.from_agent='system'
		  AND m.content LIKE '[Completed] researcher:%'`, "swarm-e2e-thread").Scan(&completedAnnouncements); err != nil {
		t.Fatal(err)
	}
	if completedAnnouncements != 1 {
		t.Fatalf("completed announcements = %d", completedAnnouncements)
	}
	var leadTokens, subagentTokens int
	if err := pool.QueryRow(ctx, `
		SELECT c.lead_tokens,c.subagent_tokens
		FROM agent_run_completion c
		JOIN agent_run r ON r.id=c.run_id
		WHERE r.thread_id=$1
		ORDER BY c.completed_at DESC LIMIT 1`, "swarm-e2e-thread").Scan(&leadTokens, &subagentTokens); err != nil {
		t.Fatal(err)
	}
	if leadTokens <= 0 || subagentTokens <= 0 {
		t.Fatalf("usage attribution = lead:%d subagent:%d", leadTokens, subagentTokens)
	}
}

func approvePlanReview(t *testing.T, serverURL string, payload map[string]any) {
	t.Helper()
	questionID, _ := payload["id"].(string)
	runID, _ := payload["run_id"].(string)
	threadID, _ := payload["thread_id"].(string)
	if questionID == "" || runID == "" || threadID == "" {
		t.Fatalf("invalid plan review payload: %#v", payload)
	}
	response, err := http.Post(
		fmt.Sprintf("%s/api/v1/threads/%s/runs/%s/questions/%s/answer", serverURL, threadID, runID, questionID),
		"application/json",
		strings.NewReader(`{"selected":["Approve"]}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("plan approval status = %s body=%s", response.Status, body)
	}
}

func writeToolCallStream(w http.ResponseWriter, responseID, callID, name string, args map[string]any, input, output int) error {
	arguments, err := json.Marshal(args)
	if err != nil {
		return err
	}
	if err := writeProviderEvent(w, map[string]any{
		"id": responseID, "model": "stub",
		"choices": []any{map[string]any{"delta": map[string]any{
			"tool_calls": []any{map[string]any{
				"index": 0, "id": callID,
				"function": map[string]any{"name": name, "arguments": string(arguments)},
			}},
		}}},
	}); err != nil {
		return err
	}
	if err := writeProviderEvent(w, map[string]any{
		"id": responseID, "model": "stub",
		"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "tool_calls"}},
		"usage":   map[string]any{"prompt_tokens": input, "completion_tokens": output},
	}); err != nil {
		return err
	}
	_, err = fmt.Fprint(w, "data: [DONE]\n\n")
	return err
}

func writeTextStream(w http.ResponseWriter, responseID, content string, input, output int) error {
	if err := writeProviderEvent(w, map[string]any{
		"id": responseID, "model": "stub",
		"choices": []any{map[string]any{"delta": map[string]any{"content": content}}},
	}); err != nil {
		return err
	}
	if err := writeProviderEvent(w, map[string]any{
		"id": responseID, "model": "stub",
		"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": input, "completion_tokens": output},
	}); err != nil {
		return err
	}
	_, err := fmt.Fprint(w, "data: [DONE]\n\n")
	return err
}

func writeProviderEvent(w http.ResponseWriter, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", raw)
	return err
}

func startAgentdPostgres(t *testing.T) (string, *pgxpool.Pool) {
	t.Helper()
	dockerPool, err := dockertest.NewPool("")
	if err != nil {
		t.Skipf("Docker unavailable: %v", err)
	}
	dockerPool.MaxWait = 60 * time.Second
	resource, err := dockerPool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres",
		Tag:        "17-alpine",
		Env:        []string{"POSTGRES_PASSWORD=postgres", "POSTGRES_USER=postgres", "POSTGRES_DB=postgres"},
	})
	if err != nil {
		t.Skipf("starting PostgreSQL container: %v", err)
	}
	_ = resource.Expire(180)
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
