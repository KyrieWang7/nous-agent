package builtin

import (
	"context"
	"strings"

	mem "github.com/KyrieWang7/nous-agent/agent-go/pkg/memory"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
)

const NameMemory = "memory"

type Memory struct{ manager *mem.Manager }

func NewMemory(manager *mem.Manager) *Memory { return &Memory{manager: manager} }
func (m *Memory) Name() string               { return NameMemory }
func (m *Memory) Grade() middleware.Grade    { return middleware.GradeListener }
func (m *Memory) BeforeAgent(ctx context.Context, st *middleware.State) error {
	if m.manager == nil {
		return nil
	}
	facts, err := m.manager.Prompt(ctx, st.ThreadID)
	if err != nil {
		return err
	}
	if facts != "" {
		st.SystemPrompt = strings.TrimSpace(st.SystemPrompt + "\n\n" + facts)
	}
	return nil
}
func (m *Memory) AfterAgent(ctx context.Context, st *middleware.State) error {
	if m.manager == nil || st.History == nil {
		return nil
	}
	return m.manager.Extract(ctx, st.ThreadID, st.History.All())
}
