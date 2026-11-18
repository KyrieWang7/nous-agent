//go:build docker

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	domain "github.com/KyrieWang7/nous-agent/agent-go/internal/model"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/repository"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/server"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/harness"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/migrations"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	metadatacontract "github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/metadata"
	runtimepostgres "github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/postgres"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/subagent"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
	platformdb "github.com/KyrieWang7/nous-agent/platform-go/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

type sessionE2ESampler func(context.Context, *lifecycle.State) (*model.Response, bool, error)

func (f sessionE2ESampler) Sample(ctx context.Context, st *lifecycle.State) (*model.Response, bool, error) {
	return f(ctx, st)
}

func TestPersistentChildHTTPControlAndContinuation(t *testing.T) {
	url, pool := startAgentdPostgres(t)
	dir, _ := filepath.Abs("../../migrations")
	if err := migrations.Up(url, dir); err != nil {
		t.Fatal(err)
	}
	db := testGORM(t, pool)
	store, err := repository.NewPostgresStore(db)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := runtimepostgres.NewSubagentStore(db)
	if err != nil {
		t.Fatal(err)
	}
	events, err := runtimepostgres.NewEventStore(db)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := runtimepostgres.NewMetadataStore(db)
	if err != nil {
		t.Fatal(err)
	}
	// A lost lock's old execution cannot overwrite a newer owner's projection.
	if _, err = pool.Exec(context.Background(), `INSERT INTO agent_thread(id) VALUES('writer-fence')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"old-writer", "new-writer"} {
		if err = store.CreateRun(context.Background(), domain.Run{RunID: id, ThreadID: "writer-fence", Status: "running", Created: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	staleCtx := metadatacontract.WithWriter(context.Background(), "old-writer")
	if err = metadata.SaveHistory(staleCtx, "writer-fence", []message.Message{{Role: message.RoleUser, Content: "stale"}}, true); !errors.Is(err, metadatacontract.ErrStaleWriter) {
		t.Fatalf("stale history writer=%v", err)
	}
	if err = metadata.SaveThreadValues(staleCtx, "writer-fence", map[string]any{"title": "stale"}); !errors.Is(err, metadatacontract.ErrStaleWriter) {
		t.Fatalf("stale state writer=%v", err)
	}
	currentCtx := metadatacontract.WithWriter(context.Background(), "new-writer")
	if err = metadata.SaveHistory(currentCtx, "writer-fence", []message.Message{{Role: message.RoleUser, Content: "current"}}, true); err != nil {
		t.Fatal(err)
	}
	// Admission uses a separate session, so even a one-connection query pool
	// can make progress while a run owns the thread lock.
	smallConfig, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	smallConfig.MaxConns = 1
	smallPool, err := pgxpool.NewWithConfig(context.Background(), smallConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer smallPool.Close()
	smallDB, err := platformdb.FromPool(smallPool)
	if err != nil {
		t.Fatal(err)
	}
	defer smallDB.Close()
	admission, err := repository.NewPostgresStore(smallDB)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := admission.AcquireThread(context.Background(), "admission-test")
	if err != nil {
		t.Fatal(err)
	}
	bounded, stop := context.WithTimeout(context.Background(), time.Second)
	if err = smallPool.Ping(bounded); err != nil {
		stop()
		t.Fatal("query pool was exhausted by admission", err)
	}
	stop()
	if _, err = store.AcquireThread(context.Background(), "admission-test"); !errors.Is(err, repository.ErrThreadBusy) {
		t.Fatalf("parallel admission=%v", err)
	}
	var lockPID int
	if err = pool.QueryRow(context.Background(), `SELECT pid FROM pg_locks WHERE locktype='advisory' AND granted LIMIT 1`).Scan(&lockPID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(context.Background(), `SELECT pg_terminate_backend($1)`, lockPID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-lease.Context.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("lost admission session did not cancel execution")
	}
	_ = lease.Release()
	lease, err = store.AcquireThread(context.Background(), "admission-test")
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err = lease.Release(); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	allowTool := make(chan struct{})
	cancelStarted := make(chan struct{})
	cleanupStarted := make(chan struct{})
	allowCleanup := make(chan struct{})
	var builds atomic.Int32
	manager := subagent.NewManagerWithPreparedFactory(func(subagent.Definition, []string) (subagent.PreparedRunner, error) {
		build := builds.Add(1)
		child, err := harness.New(harness.Options{Tools: []tool.Definition{{Name: "hold", Group: "test", Description: "hold test", Handler: func(ctx context.Context, _ tool.Call) (*tool.Result, error) {
			close(started)
			select {
			case <-allowTool:
				return &tool.Result{Content: "held"}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}}}, Sampler: sessionE2ESampler(func(ctx context.Context, st *lifecycle.State) (*model.Response, bool, error) {
			if build == 1 && st.Iteration == 0 {
				return &model.Response{StopReason: model.StopReasonToolCalls, Message: message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "hold-1", Name: "hold", Arguments: []byte(`{}`)}}}}, false, nil
			}
			raw, _ := json.Marshal(st.ModelInput.Messages)
			if !strings.Contains(string(raw), "durable steering") {
				return nil, false, errors.New("steering was not restored/admitted")
			}
			if build == 2 {
				close(cancelStarted)
				<-ctx.Done()
				return nil, false, ctx.Err()
			}
			return &model.Response{StopReason: model.StopReasonStop, Message: message.Message{Role: message.RoleAssistant, Content: "child answer"}}, false, nil
		})})
		if err != nil {
			return subagent.PreparedRunner{}, err
		}
		return subagent.PreparedRunner{Runner: child.Runner(), Release: func() error {
			if build == 2 {
				close(cleanupStarted)
				<-allowCleanup
			}
			return child.Close()
		}}, nil
	}, sessions, time.Hour)
	defer manager.Close()
	profile := subagent.Definition{Name: "worker", Description: "controlled worker", AllowedTools: []string{"hold"}, MaxTurns: 4}
	if err = manager.Register(profile); err != nil {
		t.Fatal(err)
	}
	root, err := harness.New(harness.Options{Model: faux.New(faux.ToolCall("task", `{"description":"child","prompt":"initial work","subagent_type":"worker"}`), faux.Text("root done")), Tools: []tool.Definition{manager.TaskTool(), manager.ControlTool(), {Name: "hold", Group: "test", Description: "hold test", Handler: func(context.Context, tool.Call) (*tool.Result, error) { return &tool.Result{}, nil }}}, CapabilityValues: []capability.Value{{Definition: capability.Definition{Name: "agent.worker", Kind: capability.KindAgent, Scope: capability.ScopeRun}, Value: profile}}})
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	names := root.Runtime().Generation.Capabilities().Names()
	agent := harness.Agent{Runner: root.Runner(), Capabilities: root.Runtime().Generation.Capabilities(), GenerationID: "http-test", AllowedCapabilities: names, Delegate: manager.Resume}
	registry, err := runtime.NewRegistry(context.Background(), runtime.RegistryOptions{Backend: runtimepostgres.NewRegistryBackend(db, "worker-a"), Owner: "worker-a"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := server.New(server.Options{Agent: agent, Store: store, MetadataStore: metadata, EventStore: events, Subagents: sessions, Registry: registry, OwnRegistry: true, AllowedTools: []string{"task", "subagent_control", "hold"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	httpServer := httptest.NewServer(app)
	defer httpServer.Close()
	defer func() {
		select {
		case <-allowTool:
		default:
			close(allowTool)
		}
		select {
		case <-allowCleanup:
		default:
			close(allowCleanup)
		}
	}()
	client := &http.Client{Timeout: 15 * time.Second}
	request := func(method, path, body string, status int) []byte {
		t.Helper()
		req, _ := http.NewRequest(method, httpServer.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		raw, e := io.ReadAll(resp.Body)
		if e != nil {
			t.Fatal(e)
		}
		if resp.StatusCode != status {
			t.Fatalf("%s %s status=%d body=%s", method, path, resp.StatusCode, raw)
		}
		return raw
	}
	request("POST", "/api/v1/threads", `{"thread_id":"http-thread"}`, 200)
	rootDone := make(chan error, 1)
	go func() {
		resp, e := client.Post(httpServer.URL+"/api/v1/threads/http-thread/runs", "application/json", strings.NewReader(`{"input":{"messages":[{"type":"human","content":"delegate"}]},"on_disconnect":"continue"}`))
		if e == nil {
			_, e = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		rootDone <- e
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("child did not start")
	}
	var list []struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(request("GET", "/api/v1/threads/http-thread/subagents", "", 200), &list); err != nil || len(list) != 1 {
		t.Fatalf("sessions=%v err=%v", list, err)
	}
	base := "/api/v1/threads/http-thread/subagents/" + list[0].ID
	request("POST", "/api/v1/threads/http-thread/runs", `{"input":{"messages":[{"type":"human","content":"race"}]}}`, 409)
	request("POST", base+"/wait?timeout_seconds=1", `{}`, 202)
	request("POST", base+"/messages", `{"id":"steer-1","content":"durable steering"}`, 202)
	request("POST", base+"/messages", `{"id":"steer-1","content":"durable steering"}`, 202)
	request("POST", base+"/messages", `{"id":"steer-1","content":"different content"}`, 409)
	request("POST", base+"/continue", `{"prompt":"race"}`, 409)
	request("GET", "/api/v1/threads/not-owner/subagents/"+list[0].ID, "", 404)
	close(allowTool)
	if err = <-rootDone; err != nil {
		t.Fatal(err)
	}
	if raw := request("POST", base+"/wait", `{}`, 200); !strings.Contains(string(raw), `"status":"completed"`) {
		t.Fatalf("not completed: %s", raw)
	}
	if raw := request("GET", base+"/messages", "", 200); !strings.Contains(string(raw), `"status":"consumed"`) {
		t.Fatalf("not consumed: %s", raw)
	}
	continued := request("POST", base+"/continue", `{"prompt":"continue child"}`, 202)
	var run struct {
		RunID string `json:"run_id"`
	}
	if err = json.Unmarshal(continued, &run); err != nil || run.RunID == "" {
		t.Fatalf("run=%s err=%v", continued, err)
	}
	select {
	case <-cancelStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("continuation did not restore history")
	}
	request("POST", base+"/cancel", `{}`, 202)
	select {
	case <-cleanupStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("durable cancel was not delivered")
	}
	if raw := request("GET", base, "", 200); !strings.Contains(string(raw), `"status":"running"`) {
		t.Fatalf("terminal before cleanup: %s", raw)
	}
	close(allowCleanup)
	if raw := request("POST", base+"/wait", `{}`, 200); !strings.Contains(string(raw), `"status":"cancelled"`) {
		t.Fatalf("cancel outcome: %s", raw)
	}
	// Persistent cancellation reaches another registry instance without Redis.
	other, err := runtime.NewRegistry(context.Background(), runtime.RegistryOptions{Backend: runtimepostgres.NewRegistryBackend(db, "worker-b"), Owner: "worker-b"})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err = pool.Exec(context.Background(), `INSERT INTO agent_run(id,thread_id,status) VALUES('remote-run','http-thread','running')`); err != nil {
		t.Fatal(err)
	}
	remoteCtx, err := registry.Register(context.Background(), runtime.RunRecord{RunID: "remote-run", ThreadID: "http-thread"})
	if err != nil {
		t.Fatal(err)
	}
	if err = other.Cancel(context.Background(), "remote-run"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-remoteCtx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("cross-instance cancel lost")
	}
	_ = registry.Complete(context.Background(), "remote-run", runtime.StatusCancelled)
}
