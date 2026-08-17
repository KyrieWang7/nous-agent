package handlers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/swarm"
)

type recordingMailbox struct {
	calls int
	items []swarm.Message
}

func (m *recordingMailbox) Poll(context.Context, string, string, int) ([]swarm.Message, error) {
	m.calls++
	return append([]swarm.Message(nil), m.items...), nil
}

func TestInboxPollerDoesNotConsumeWhenSwarmIsDisabled(t *testing.T) {
	mailbox := &recordingMailbox{items: []swarm.Message{{From: "reviewer", Content: "done"}}}
	poller := NewInboxPoller(mailbox, 50, time.Second)
	state := lifecycle.NewState(lifecycle.StateInit{RunID: "run-1", ThreadID: "thread-1"})
	state.ModelInput = &model.Request{}
	state.SetValue("swarm_enabled", false)
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{SwarmTeamID: "team-1", SwarmAgentName: "lead"})

	if err := poller.BeforeModel(ctx, state); err != nil {
		t.Fatal(err)
	}
	if mailbox.calls != 0 {
		t.Fatalf("disabled poll count = %d, want 0", mailbox.calls)
	}
}

func TestInboxPollerThrottlesPerRunAndCleansUp(t *testing.T) {
	mailbox := &recordingMailbox{items: []swarm.Message{{From: "reviewer", Content: "done"}}}
	poller := NewInboxPoller(mailbox, 50, time.Minute)
	now := time.Unix(100, 0)
	poller.now = func() time.Time { return now }
	state := lifecycle.NewState(lifecycle.StateInit{RunID: "run-1", ThreadID: "thread-1"})
	state.ModelInput = &model.Request{}
	state.SetValue("swarm_enabled", true)
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{SwarmTeamID: "team-1", SwarmAgentName: "lead"})

	if err := poller.BeforeModel(ctx, state); err != nil {
		t.Fatal(err)
	}
	if err := poller.BeforeModel(ctx, state); err != nil {
		t.Fatal(err)
	}
	if mailbox.calls != 1 {
		t.Fatalf("poll count = %d, want 1", mailbox.calls)
	}
	if len(state.ModelInput.Messages) != 1 || !strings.Contains(state.ModelInput.Messages[0].Content, "reviewer: done") {
		t.Fatalf("injected messages = %#v", state.ModelInput.Messages)
	}
	if err := poller.AfterAgent(ctx, state); err != nil {
		t.Fatal(err)
	}
	state.ModelInput = &model.Request{}
	if err := poller.BeforeModel(ctx, state); err != nil {
		t.Fatal(err)
	}
	if mailbox.calls != 2 {
		t.Fatalf("poll count after cleanup = %d, want 2", mailbox.calls)
	}
}
