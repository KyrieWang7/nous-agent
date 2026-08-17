package handlers

import (
	"context"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
)

const NameSubagentLimit = "subagentLimit"

type SubagentLimit struct{ max int }

func NewSubagentLimit(max int) *SubagentLimit {
	if max <= 0 {
		max = 3
	}
	return &SubagentLimit{max: max}
}

func (SubagentLimit) Name() string           { return NameSubagentLimit }
func (SubagentLimit) Grade() lifecycle.Grade { return lifecycle.GradeListener }
func (m *SubagentLimit) AfterModel(_ context.Context, st *lifecycle.State) error {
	if st.ModelOutput == nil {
		return nil
	}
	calls := st.ModelOutput.Message.ToolCalls
	seen := 0
	dropped := 0
	kept := calls[:0]
	for _, call := range calls {
		if call.Name == "task" {
			seen++
			if seen > m.max {
				dropped++
				continue
			}
		}
		kept = append(kept, call)
	}
	if dropped == 0 {
		return nil
	}
	st.ModelOutput.Message.ToolCalls = kept
	if st.History != nil {
		st.History.ReplaceLastAssistant(st.ModelOutput.Message)
	}
	st.SetValue("subagent_calls_dropped", dropped)
	return nil
}
