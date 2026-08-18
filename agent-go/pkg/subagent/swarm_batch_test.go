package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/harness"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func TestSwarmBatchDispatchesWorkersConcurrentlyAndPreservesOrder(t *testing.T) {
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	manager := NewManagerWithContextFactory(func(_ context.Context, _ Definition, _ []string, req DispatchRequest) (*loop.Runner, error) {
		ready <- struct{}{}
		<-release
		h, err := harness.New(harness.Options{Model: faux.New(faux.Text("done-" + req.Name))})
		if err != nil {
			return nil, err
		}
		return h.Runner(), nil
	}, nil, 0, 2)
	registerBatchProfiles(t, manager)

	resultCh := make(chan *tool.Result, 1)
	go func() {
		result, _ := manager.SwarmBatchTool().Handler(batchTestContext(), tool.Call{ID: "batch-1", Args: []byte(`{
			"objective":"compare evidence",
			"items":[
				{"id":"b","name":"second","description":"second","prompt":"second","subagent_type":"worker"},
				{"id":"a","name":"first","description":"first","prompt":"first","subagent_type":"worker"}
			]
		}`)})
		resultCh <- result
	}()

	<-ready
	<-ready
	close(release)
	result := <-resultCh
	batch := decodeBatchToolResult(t, result)
	if batch.Status != "completed" {
		t.Fatalf("status = %q, workers=%#v", batch.Status, batch.Workers)
	}
	if len(batch.Workers) != 2 || batch.Workers[0].ID != "b" || batch.Workers[0].Result.Output != "done-second" || batch.Workers[1].ID != "a" || batch.Workers[1].Result.Output != "done-first" {
		t.Fatalf("workers = %#v", batch.Workers)
	}
	if batch.Workers[0].Result.TaskID != "batch-1:b" || batch.Workers[1].Result.TaskID != "batch-1:a" {
		t.Fatalf("task ids = %q, %q", batch.Workers[0].Result.TaskID, batch.Workers[1].Result.TaskID)
	}
}

func TestSwarmBatchReportsPartialFailureAndRunsReviewer(t *testing.T) {
	var mu sync.Mutex
	var reviewPrompt string
	manager := NewManagerWithContextFactory(func(_ context.Context, _ Definition, _ []string, req DispatchRequest) (*loop.Runner, error) {
		if req.Name == "broken" {
			return nil, errors.New("worker unavailable")
		}
		if req.Name == "reviewer" {
			mu.Lock()
			reviewPrompt = req.Prompt
			mu.Unlock()
		}
		h, err := harness.New(harness.Options{Model: faux.New(faux.Text("done-" + req.Name))})
		if err != nil {
			return nil, err
		}
		return h.Runner(), nil
	}, nil, 0, 2)
	registerBatchProfiles(t, manager)

	result, err := manager.SwarmBatchTool().Handler(batchTestContext(), tool.Call{ID: "batch-2", Args: []byte(`{
		"objective":"produce a checked answer",
		"items":[
			{"id":"ok","name":"worker-ok","description":"ok","prompt":"collect","subagent_type":"worker"},
			{"id":"bad","name":"broken","description":"bad","prompt":"collect","subagent_type":"worker"}
		],
		"review":{"enabled":true}
	}`)})
	if err != nil {
		t.Fatal(err)
	}
	batch := decodeBatchToolResult(t, result)
	if batch.Status != "partial" || batch.Review == nil || batch.Review.Status != "completed" || batch.Review.Output != "done-reviewer" {
		t.Fatalf("batch = %#v", batch)
	}
	mu.Lock()
	gotPrompt := reviewPrompt
	mu.Unlock()
	for _, want := range []string{"produce a checked answer", "done-worker-ok", "worker unavailable"} {
		if !strings.Contains(gotPrompt, want) {
			t.Fatalf("review prompt is missing %q: %s", want, gotPrompt)
		}
	}
}

func TestSwarmBatchRejectsOversizedOrDuplicateBatchBeforeDispatch(t *testing.T) {
	manager := NewManager(nil, nil, 0, 2)
	registerBatchProfiles(t, manager)
	result, err := manager.SwarmBatchTool().Handler(batchTestContext(), tool.Call{Args: []byte(`{
		"objective":"duplicate",
		"items":[
			{"id":"same","name":"one","description":"one","prompt":"one","subagent_type":"worker"},
			{"id":"same","name":"two","description":"two","prompt":"two","subagent_type":"worker"}
		]
	}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content, "duplicate swarm_batch item id") {
		t.Fatalf("result = %#v", result)
	}
}

func registerBatchProfiles(t *testing.T, manager *Manager) {
	t.Helper()
	for _, definition := range []Definition{
		{Name: "worker", Description: "worker", MaxTurns: 5},
		{Name: "verification", Description: "reviewer", MaxTurns: 5},
	} {
		if err := manager.Register(definition); err != nil {
			t.Fatal(err)
		}
	}
}

func batchTestContext() context.Context {
	return testRunContext(context.Background(), runtime.RunContext{RunID: "run", ThreadID: "thread"})
}

func decodeBatchToolResult(t *testing.T, result *tool.Result) swarmBatchResult {
	t.Helper()
	if result == nil || result.IsError {
		t.Fatalf("tool result = %#v", result)
	}
	var batch swarmBatchResult
	if err := json.Unmarshal([]byte(strings.TrimPrefix(result.Content, "Swarm batch result: ")), &batch); err != nil {
		t.Fatal(err)
	}
	return batch
}
