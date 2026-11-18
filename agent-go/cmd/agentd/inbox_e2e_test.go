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
	"sync"
	"testing"
	"time"

	domain "github.com/KyrieWang7/nous-agent/agent-go/internal/model"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/repository"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/server"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/harness"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/migrations"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/inbox"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/metadata"
	runtimepostgres "github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/postgres"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func TestRootInboxDurableAdmissionQueueSteeringAndRecovery(t *testing.T) {
	ctx := context.Background()
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
	entered := make(chan struct{}, 4)
	cancelEntered := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var mu sync.Mutex
	calls := []string{}
	h, err := harness.New(harness.Options{Sampler: sessionE2ESampler(func(_ context.Context, st *lifecycle.State) (*model.Response, bool, error) {
		text := ""
		for _, m := range st.ModelInput.Messages {
			if m.Role == message.RoleUser {
				text += m.Content + "|"
			}
		}
		mu.Lock()
		calls = append(calls, text)
		mu.Unlock()
		if strings.HasSuffix(text, "hold|") || strings.HasSuffix(text, "cancel test|") {
			return &model.Response{StopReason: model.StopReasonToolCalls, Message: message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "gate", Name: "hold", Arguments: []byte(`{}`)}}}}, false, nil
		}
		return &model.Response{StopReason: model.StopReasonStop, Message: message.Message{Role: message.RoleAssistant, Content: "answer:" + text}}, false, nil
	}), Tools: []tool.Definition{{Name: "hold", Group: "test", Description: "wait for test admission", Handler: func(ctx context.Context, _ tool.Call) (*tool.Result, error) {
		if run, ok := runtime.RunContextFrom(ctx); ok && run.ThreadID == "cancel-thread" {
			cancelEntered <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		}
		entered <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return &tool.Result{Content: "released"}, nil
		}
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	events, err := runtimepostgres.NewEventStore(db)
	if err != nil {
		t.Fatal(err)
	}
	values, err := runtimepostgres.NewMetadataStore(db)
	if err != nil {
		t.Fatal(err)
	}
	makeServer := func() (*server.Server, *httptest.Server) {
		owner := domain.NewID()
		reg, e := runtime.NewRegistry(ctx, runtime.RegistryOptions{Backend: runtimepostgres.NewRegistryBackend(db, owner), Owner: owner})
		if e != nil {
			t.Fatal(e)
		}
		app, e := server.New(server.Options{Agent: harness.Agent{Runner: h.Runner(), Capabilities: h.Runtime().Generation.Capabilities(), AllowedCapabilities: h.Runtime().Generation.Capabilities().Names(), GenerationID: "inbox-test"}, Store: store, MetadataStore: values, EventStore: events, Registry: reg, OwnRegistry: true, AllowedTools: []string{"hold"}})
		if e != nil {
			t.Fatal(e)
		}
		return app, httptest.NewServer(app)
	}
	app, httpServer := makeServer()
	sibling, siblingHTTP := makeServer()
	defer siblingHTTP.Close()
	defer sibling.Close()
	defer func() { httpServer.Close(); app.Close() }()
	request := func(method, path, body string, status int) []byte {
		t.Helper()
		req, _ := http.NewRequest(method, httpServer.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, e := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		raw, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		if res.StatusCode != status {
			t.Fatalf("%s %s status=%d body=%s", method, path, res.StatusCode, raw)
		}
		return raw
	}
	send := func(tid, id, delivery, prompt string) inbox.Entry {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"id": id, "delivery": delivery, "input": map[string]any{"messages": []any{map[string]any{"role": "user", "content": prompt}}}})
		raw := request("POST", "/api/v1/threads/"+tid+"/messages", string(body), 202)
		var e inbox.Entry
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	wait := func(tid, id, status string) inbox.Entry {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			e, err := store.GetInput(ctx, tid, id)
			if err != nil {
				t.Fatal(err)
			}
			if e.Status == status && (status != "admitted" || e.RunStatus == "success") {
				return e
			}
			time.Sleep(20 * time.Millisecond)
		}
		last, _ := store.GetInput(ctx, tid, id)
		ev, _ := events.Get(ctx, last.RunID, 0, 100)
		for _, v := range ev {
			if v.Type == runtime.EventRunEnd {
				t.Log(string(v.Data))
			}
		}
		t.Fatalf("input %s did not settle as %s: %+v", id, status, last)
		return inbox.Entry{}
	}
	send("queue-thread", "first", "next_turn", "hold")
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		e, _ := store.GetInput(ctx, "queue-thread", "first")
		ev, _ := events.Get(ctx, e.RunID, 0, 100)
		for _, v := range ev {
			if v.Type == runtime.EventRunEnd {
				t.Log(string(v.Data))
			}
		}
		t.Fatalf("initial tool did not start: entry=%+v", e)
	}
	first, err := store.GetInput(ctx, "queue-thread", "first")
	if err != nil || first.Status != "admitted" {
		t.Fatalf("initial ACK=%+v err=%v", first, err)
	}
	send("queue-thread", "second", "next_turn", "second")
	send("queue-thread", "third", "next_turn", "never execute")
	request("POST", "/api/v1/threads/queue-thread/messages/third/cancel", "", 200)
	steer := send("queue-thread", "guidance", "next_step", "steer")
	if steer.TargetRunID != first.RunID {
		t.Fatalf("guidance not bound to current run: %+v", steer)
	}
	request("POST", "/api/v1/threads/queue-thread/messages", `{"id":"guidance","delivery":"next_step","input":{"messages":[{"role":"user","content":"different"}]}}`, 409)
	request("POST", "/api/v1/threads/queue-thread/messages", `{"id":"invalid","delivery":"next_step","input":{"messages":[{"role":"user","content":"bad"}]},"context":{"mode":"ultra"}}`, 400)
	once.Do(func() { close(release) })
	wait("queue-thread", "first", "admitted")
	second := wait("queue-thread", "second", "admitted")
	wait("queue-thread", "guidance", "admitted")
	wait("queue-thread", "third", "cancelled")
	// Identical retries return the original ID after terminal admission.
	if retry := send("queue-thread", "second", "next_turn", "second"); retry.RunID != second.RunID {
		t.Fatal("retry created another run")
	}
	request("POST", "/api/v1/threads/queue-thread/messages/second/cancel", "", 409)
	mu.Lock()
	got := append([]string(nil), calls...)
	mu.Unlock()
	if len(got) != 3 || got[0] != "hold|" || got[1] != "hold|steer|" || got[2] != "hold|steer|second|" {
		t.Fatalf("model-visible sequence=%v", got)
	}
	history, err := store.LoadHistory(ctx, "queue-thread")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) == 0 {
		t.Fatal("ACK without canonical transcript")
	}
	// A cancelled input cannot partially commit the proposed transcript.
	before := history
	if err = store.Admit(ctx, "queue-thread", second.RunID, []string{"third"}, []message.Message{{Role: message.RoleUser, Content: "must rollback"}}); err == nil {
		t.Fatal("cancelled message admitted")
	}
	history, err = store.LoadHistory(ctx, "queue-thread")
	if err != nil || len(history) != len(before) || history[0].Content != before[0].Content {
		t.Fatal("failed ACK modified transcript")
	}
	if err = store.Admit(ctx, "queue-thread", first.RunID, []string{"second"}, nil); !errors.Is(err, metadata.ErrStaleWriter) {
		t.Fatalf("stale admission=%v", err)
	}
	// Cross-instance cancellation settles the current run and its pending
	// steering, while independent next-turn work continues automatically.
	send("cancel-thread", "cancel-current", "next_turn", "cancel test")
	select {
	case <-cancelEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("cancellation tool did not start")
	}
	current, err := store.GetInput(ctx, "cancel-thread", "cancel-current")
	if err != nil {
		t.Fatal(err)
	}
	send("cancel-thread", "independent", "next_turn", "independent task")
	send("cancel-thread", "cancel-guidance", "next_step", "do not deliver after cancellation")
	request("POST", "/api/v1/threads/cancel-thread/runs/"+current.RunID+"/cancel", "", 200)
	wait("cancel-thread", "independent", "admitted")
	wait("cancel-thread", "cancel-guidance", "cancelled")
	cancelled, err := store.GetRun(ctx, current.RunID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("current run=%+v err=%v", cancelled, err)
	}
	// Simulate death before ACK, with no provider call. A fresh service fences
	// the old writer and safely executes the durable claim once.
	httpServer.Close()
	app.Close()
	siblingHTTP.Close()
	sibling.Close()
	_, err = store.CreateThread(ctx, domain.Thread{ThreadID: "recover-thread", Created: time.Now().UTC()}, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Enqueue(ctx, inbox.Entry{ID: "recover", ThreadID: "recover-thread", Delivery: inbox.NextTurn, Input: inbox.Input{Prompt: "recover once"}})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquireThread(ctx, "recover-thread")
	if err != nil {
		t.Fatal(err)
	}
	claim, found, err := store.Claim(ctx, "recover-thread", "dead-before-admission")
	if err != nil || !found || claim.Status != "claimed" {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	if err = lease.Release(); err != nil {
		t.Fatal(err)
	}
	app, httpServer = makeServer()
	recovered := wait("recover-thread", "recover", "admitted")
	if recovered.RunID == claim.RunID {
		t.Fatal("recovery reused stale writer")
	}
	dead, err := store.GetRun(ctx, claim.RunID)
	if err != nil || dead.Status != "interrupted" {
		t.Fatalf("orphan=%+v err=%v", dead, err)
	}
	mu.Lock()
	count := 0
	for _, s := range calls {
		if s == "recover once|" {
			count++
		}
	}
	mu.Unlock()
	if count != 1 {
		t.Fatalf("recovered provider calls=%d", count)
	}
	// At an idle turn boundary, claim every next-step message before one
	// next-turn input, even when the guidance was enqueued later.
	httpServer.Close()
	app.Close()
	_, err = store.CreateThread(ctx, domain.Thread{ThreadID: "batch-thread", Created: time.Now().UTC()}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []inbox.Entry{{ID: "turn", ThreadID: "batch-thread", Delivery: inbox.NextTurn, Input: inbox.Input{Prompt: "batch task"}}, {ID: "guide-one", ThreadID: "batch-thread", Delivery: inbox.NextStep, Input: inbox.Input{Prompt: "guide one"}}, {ID: "guide-two", ThreadID: "batch-thread", Delivery: inbox.NextStep, Input: inbox.Input{Prompt: "guide two"}}} {
		if _, err = store.Enqueue(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	app, httpServer = makeServer()
	batch := wait("batch-thread", "turn", "admitted")
	for _, id := range []string{"guide-one", "guide-two"} {
		if e := wait("batch-thread", id, "admitted"); e.RunID != batch.RunID {
			t.Fatalf("guidance split into another turn: %+v", e)
		}
	}
	mu.Lock()
	matched := false
	for _, s := range calls {
		if s == "guide one|guide two|batch task|" {
			matched = true
		}
	}
	mu.Unlock()
	if !matched {
		t.Fatal("DSH next-step-before-next-turn ordering was not observed")
	}
	// Admission is durable even when the process dies before the provider result.
	// Such input is audit history, never an automatic replay of uncertain effects.
	httpServer.Close()
	app.Close()
	_, err = store.CreateThread(ctx, domain.Thread{ThreadID: "admitted-thread", Created: time.Now().UTC()}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []inbox.Entry{{ID: "already", ThreadID: "admitted-thread", Delivery: inbox.NextTurn, Input: inbox.Input{Prompt: "already admitted"}}, {ID: "later", ThreadID: "admitted-thread", Delivery: inbox.NextTurn, Input: inbox.Input{Prompt: "later task"}}} {
		if _, err = store.Enqueue(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	lease, err = store.AcquireThread(ctx, "admitted-thread")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Claim(ctx, "admitted-thread", "dead-after-admission"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CancelInput(ctx, "admitted-thread", "already"); !errors.Is(err, inbox.ErrAdmitted) {
		t.Fatalf("withdrawal during preparation=%v", err)
	}
	if err = store.Admit(ctx, "admitted-thread", "dead-after-admission", []string{"already"}, []message.Message{{Role: message.RoleUser, Content: "already admitted"}}); err != nil {
		t.Fatal(err)
	}
	if err = lease.Release(); err != nil {
		t.Fatal(err)
	}
	app, httpServer = makeServer()
	wait("admitted-thread", "later", "admitted")
	admitted, err := store.GetInput(ctx, "admitted-thread", "already")
	if err != nil || admitted.RunID != "dead-after-admission" || admitted.Status != "admitted" || admitted.RunStatus != "interrupted" {
		t.Fatalf("admitted crash recovery=%+v err=%v", admitted, err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, s := range calls {
		if s == "already admitted|" {
			t.Fatal("automatically replayed admitted input")
		}
	}

}
