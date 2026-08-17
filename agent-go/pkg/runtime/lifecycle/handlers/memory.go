package handlers

import (
	"context"
	"fmt"
	"strings"

	mem "github.com/KyrieWang7/nous-agent/agent-go/pkg/memory"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
)

const NameMemory = "memory"

type Memory struct{ capabilityName string }

func NewMemory(capabilityName string) *Memory { return &Memory{capabilityName: capabilityName} }
func (m *Memory) Name() string                { return NameMemory }
func (m *Memory) Grade() lifecycle.Grade      { return lifecycle.GradeListener }
func (m *Memory) BeforeAgent(ctx context.Context, st *lifecycle.State) error {
	manager, err := m.resolve(ctx)
	if err != nil {
		return err
	}
	facts, err := manager.Prompt(ctx, memoryScope(ctx, st.ThreadID))
	if err != nil {
		return err
	}
	if facts != "" {
		st.SystemPrompt = strings.TrimSpace(st.SystemPrompt + "\n\n" + facts)
	}
	return nil
}
func (m *Memory) AfterAgent(ctx context.Context, st *lifecycle.State) error {
	if st.History == nil {
		return nil
	}
	manager, err := m.resolve(ctx)
	if err != nil {
		return err
	}
	return manager.Extract(ctx, memoryScope(ctx, st.ThreadID), st.History.All())
}

func (m *Memory) resolve(ctx context.Context) (*mem.Manager, error) {
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || !run.Capabilities.Initialized() {
		return nil, fmt.Errorf("memory: capability view is not initialized")
	}
	return capability.ResolveViewAs[*mem.Manager](ctx, run.Capabilities, m.capabilityName, capability.KindMemory, capability.ResolveRequest{
		GenerationID: run.GenerationID, ThreadID: run.ThreadID, RunID: run.RunID, Values: run.Values,
	})
}

func memoryScope(ctx context.Context, threadID string) mem.Scope {
	scope := mem.Scope{ThreadID: threadID}
	if run, ok := runtime.RunContextFrom(ctx); ok {
		scope.UserID, _ = run.Values["user_id"].(string)
		scope.ProjectID, _ = run.Values["project_id"].(string)
	}
	return scope
}
