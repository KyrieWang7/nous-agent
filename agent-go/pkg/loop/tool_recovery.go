package loop

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// toolStepRecovery belongs to one accepted assistant step. Dispatches record
// facts concurrently; settlement runs only after the executor has drained them.
// Raw model-output audit records do not by themselves admit a transcript step.
type toolStepRecovery struct {
	mu              sync.Mutex
	run             runtime.RunContext
	watermark       uint64
	started         map[string]bool
	results         map[string]recoveredToolResult
	dispatched      bool
	trackedDispatch bool
}

type recoveredToolResult struct {
	message message.Message
	durable bool
}

func newToolStepRecovery(ctx context.Context, h *message.History) *toolStepRecovery {
	run, _ := runtime.RunContextFrom(ctx)
	return &toolStepRecovery{run: run, watermark: h.Watermark(), started: map[string]bool{}, results: map[string]recoveredToolResult{}}
}

func (s *toolStepRecovery) recordStart(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started[id] = true
}

func (s *toolStepRecovery) recordResult(m message.Message, durable bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A committed result is authoritative even if the executor later fails to
	// return it, or a custom executor returns a different view of the result.
	if previous, ok := s.results[m.ToolCallID]; ok && previous.durable {
		return
	}
	s.results[m.ToolCallID] = recoveredToolResult{message: message.Clone(m), durable: durable}
}

func (s *toolStepRecovery) settle(ctx context.Context, history *message.History) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Honor governance that removed/replaced tool calls, and never repair an
	// older step merely because it was present when this run started.
	var calls []message.ToolCall
	answered := map[string]bool{}
	for _, m := range history.Since(s.watermark) {
		calls = append(calls, m.ToolCalls...)
		if m.Role == message.RoleTool {
			answered[m.ToolCallID] = true
		}
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	observer := &runtimeToolTransactionObserver{run: s.run}
	for _, call := range calls {
		if answered[call.ID] {
			continue
		}
		result, ok := s.results[call.ID]
		if !ok {
			// A custom executor without the native transaction contract cannot
			// establish that an unreported call never ran.
			unknown := s.started[call.ID] || (s.dispatched && !s.trackedDispatch)
			code := "TOOL_NOT_STARTED"
			text := "The tool call was not started by the Harness before this step ended. Retry it if it is still needed."
			if unknown {
				code = "TOOL_OUTCOME_UNKNOWN"
				text = "The tool call may have run, but no result was durably recorded. Its outcome is unknown. Do not retry blindly: retry only if the operation is read-only or idempotent; otherwise first verify external state or ask the user."
			}
			result.message = message.Message{Role: message.RoleTool, ToolCallID: call.ID, Name: call.Name, Content: text, IsError: true, AdditionalKwargs: map[string]any{"tool_recovery_code": code}}
		}
		if !result.durable {
			if err := observer.publishMessage(cleanup, result.message); err != nil {
				return fmt.Errorf("loop: settling tool result %q: %w", call.ID, err)
			}
		}
		history.Append(result.message)
		answered[call.ID] = true
	}
	return nil
}

func toolOutcomeMessage(o tool.Outcome) message.Message {
	m := message.Message{Role: message.RoleTool, ToolCallID: o.Call.ID, Name: o.Call.Name, IsError: o.ExecErr != nil}
	if o.Result != nil {
		m.Content = o.Result.Content
		m.ContentBlocks = o.Result.ContentBlocks
		m.IsError = m.IsError || o.Result.IsError
		m.AdditionalKwargs = cloneAdditionalKwargs(o.Result.AdditionalKwargs)
	}
	if m.Content == "" && o.ExecErr != nil {
		m.Content = o.ExecErr.Error()
	}
	if m.Name == "task" && o.ExecErr != nil {
		if _, ok := m.AdditionalKwargs[message.SubagentStatusKey]; !ok {
			m.Content = "Task failed. Error: " + o.ExecErr.Error()
			m.AdditionalKwargs = message.MakeSubagentAdditionalKwargs(message.SubagentFailed, o.ExecErr.Error())
		}
	}
	return m
}
