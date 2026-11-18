package loop_test

import (
	"context"
	"errors"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func TestInboxAdmissionStopsBeforeProviderOnCommitFailure(t *testing.T) {
	registry := tool.NewRegistry()
	history := message.NewHistory()
	history.Load([]message.Message{{Role: message.RoleUser, Content: "old input"}, {Role: message.RoleAssistant, Content: "old answer"}})
	calls := 0
	checkpoints := 0
	commitErr := errors.New("inbox transaction failed")
	runner, err := loop.NewRunner(loop.Config{Registry: registry, Executor: tool.NewExecutor(registry, tool.ExecutorOptions{}), Sampler: samplerFunc(func(context.Context, *lifecycle.State) (*model.Response, bool, error) {
		calls++
		return nil, false, errors.New("must not run")
	})})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Run(context.Background(), loop.Request{ThreadID: "thread", RunID: "run", Prompt: "next", History: history,
		Checkpoint: func(context.Context, []message.Message) error { checkpoints++; return nil },
		PullInputs: func(context.Context) ([]message.Message, func(context.Context, []message.Message) error, error) {
			return []message.Message{{Role: message.RoleUser, Content: "queued"}}, func(context.Context, []message.Message) error { return commitErr }, nil
		},
	})
	if !errors.Is(err, commitErr) || calls != 0 || checkpoints != 0 || history.Len() != 2 {
		t.Fatalf("err=%v calls=%d checkpoints=%d history=%v", err, calls, checkpoints, history.All())
	}
}

func TestCheckpointCommitsToolIntentBeforeExecution(t *testing.T) {
	registry := tool.NewRegistry()
	executed := false
	intentSaved := false
	if err := registry.Register(tool.Definition{Name: "effect", Group: "test", Description: "record side effect", Handler: func(context.Context, tool.Call) (*tool.Result, error) {
		executed = true
		if !intentSaved {
			t.Error("side effect preceded tool intent checkpoint")
		}
		return &tool.Result{Content: "done"}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	steps := 0
	runner, err := loop.NewRunner(loop.Config{Registry: registry, Executor: tool.NewExecutor(registry, tool.ExecutorOptions{}), Sampler: samplerFunc(func(context.Context, *lifecycle.State) (*model.Response, bool, error) {
		steps++
		if steps == 1 {
			return &model.Response{StopReason: model.StopReasonToolCalls, Message: message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "effect-1", Name: "effect", Arguments: []byte(`{}`)}}}}, false, nil
		}
		return &model.Response{StopReason: model.StopReasonStop, Message: message.Message{Role: message.RoleAssistant, Content: "finished"}}, false, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Run(context.Background(), loop.Request{RunID: "run", ThreadID: "thread", History: message.NewHistory(), Prompt: "go", Checkpoint: func(_ context.Context, history []message.Message) error {
		for _, msg := range history {
			if len(msg.ToolCalls) > 0 {
				intentSaved = true
			}
		}
		return nil
	}})
	if err != nil || !executed {
		t.Fatalf("err=%v executed=%v", err, executed)
	}
}
