package loop_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func TestAgentDriverKeepsConcurrentTurnsIsolated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	registry := tool.NewRegistry()
	runner, err := loop.NewRunner(loop.Config{
		Registry: registry, Executor: tool.NewExecutor(registry, tool.ExecutorOptions{}),
		Limits: loop.Limits{MaxIterations: 3, StopReinjectionLimit: 1},
		Sampler: samplerFunc(func(ctx context.Context, st *lifecycle.State) (*model.Response, bool, error) {
			if st.Iteration == 0 {
				ready <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return nil, false, ctx.Err()
				}
			}
			st.SetValue("owner", st.RunID)
			return &model.Response{Message: message.Message{Role: message.RoleAssistant, Content: fmt.Sprintf("%s:%d", st.RunID, st.Iteration)}, Usage: model.Usage{InputTokens: 1, OutputTokens: 1}}, false, nil
		}),
		StopGate: stopGateFunc(func(_ context.Context, _ string, st *lifecycle.State) (string, error) {
			if st.Iteration == 0 {
				return "complete one more step", nil
			}
			return "", nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		id      string
		result  *loop.Result
		history []message.Message
		err     error
	}
	finished := make(chan outcome, 2)
	for _, id := range []string{"first", "second"} {
		go func() {
			history := message.NewHistory()
			result, err := runner.Run(ctx, loop.Request{ThreadID: id, RunID: id, History: history, Prompt: id})
			finished <- outcome{id: id, result: result, history: history.All(), err: err}
		}()
	}
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("turns did not overlap")
		}
	}
	close(release)
	for range 2 {
		var got outcome
		select {
		case got = <-finished:
		case <-ctx.Done():
			t.Fatal("turn did not settle")
		}
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.result.Output != got.id+":1" || got.result.Iterations != 2 || got.result.Usage.TotalTokens() != 4 || got.result.Values["owner"] != got.id {
			t.Fatalf("run state leaked: %+v", got.result)
		}
		if len(got.history) != 4 || got.history[0].Content != got.id || got.history[1].Content != got.id+":0" || got.history[3].Content != got.id+":1" {
			t.Fatalf("history leaked: %+v", got.history)
		}
	}
}
