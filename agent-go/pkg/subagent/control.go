package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/delegation"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// ControlTool addresses only direct children. A guessed session ID cannot reach
// a sibling's descendants or another thread, including after a process restart.
func (m *Manager) ControlTool() tool.Definition {
	return tool.Definition{Name: "subagent_control", Group: "subagent", Description: "List, inspect, message, wait for, or interrupt direct child sessions. Messages are admitted at the next model step; wait timeouts do not cancel work.",
		Parameters: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["list","inspect","send","wait","interrupt"]},"session_id":{"type":"string"},"task_id":{"type":"string","description":"Current background task ID, when its session ID is not yet available."},"message_id":{"type":"string"},"content":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":30}},"required":["action"]}`), Metadata: tool.Metadata{IsAgentState: true, IsConcurrencySafe: true}, Handler: func(ctx context.Context, call tool.Call) (*tool.Result, error) {
			var args struct {
				Action    string `json:"action"`
				SessionID string `json:"session_id"`
				TaskID    string `json:"task_id"`
				MessageID string `json:"message_id"`
				Content   string `json:"content"`
				Timeout   int    `json:"timeout_seconds"`
			}
			if err := json.Unmarshal(call.Args, &args); err != nil {
				return taskFailure(err.Error()), nil
			}
			parent, ok := runtime.RunContextFrom(ctx)
			if !ok {
				return taskFailure("parent run context required"), nil
			}
			store, ok := m.store.(delegation.SessionStore)
			if !ok {
				return taskFailure("persistent sessions unavailable"), nil
			}
			if args.Action == "list" {
				sessions, err := store.ListSessions(ctx, parent.ThreadID)
				if err != nil {
					return taskFailure(err.Error()), nil
				}
				results := []Result{}
				for _, session := range sessions {
					if session.ParentSessionID == parent.SubagentSessionID {
						results = append(results, session.Result)
					}
				}
				return controlResult(results), nil
			}
			if args.SessionID == "" && args.TaskID != "" {
				result, err := m.taskStatus(ctx, args.TaskID)
				if err != nil {
					return taskFailure(err.Error()), nil
				}
				args.SessionID = result.SessionID
				if args.SessionID == "" {
					switch args.Action {
					case "interrupt":
						if err := m.Interrupt(ctx, args.TaskID); err != nil {
							return taskFailure(err.Error()), nil
						}
						return controlResult(result), nil
					case "wait":
						seconds := args.Timeout
						if seconds < 1 {
							seconds = 20
						}
						if seconds > 30 {
							seconds = 30
						}
						waitCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
						defer cancel()
						settled, err := m.Wait(waitCtx, args.TaskID)
						if errors.Is(err, context.DeadlineExceeded) {
							return controlResult(result), nil
						}
						if err != nil {
							return taskFailure(err.Error()), nil
						}
						return controlResult(settled), nil
					default:
						return controlResult(result), nil
					}
				}
			}
			session, err := store.LoadSession(ctx, parent.ThreadID, args.SessionID)
			if err != nil {
				return taskFailure(err.Error()), nil
			}
			if session.ParentSessionID != parent.SubagentSessionID {
				return taskFailure(delegation.ErrNotFound.Error()), nil
			}
			switch args.Action {
			case "inspect":
				return controlResult(map[string]any{"result": session.Result, "history": session.History}), nil
			case "interrupt":
				err = store.CancelSession(ctx, parent.ThreadID, session.ID)
			case "send":
				inbox, ok := store.(delegation.InboxStore)
				if !ok {
					return taskFailure("inbox unavailable"), nil
				}
				var input delegation.InboxMessage
				input, err = inbox.Enqueue(ctx, parent.ThreadID, session.ID, delegation.InboxMessage{ID: args.MessageID, Content: args.Content})
				if err == nil {
					return controlResult(input), nil
				}
			case "wait":
				limit := args.Timeout
				if limit < 1 {
					limit = 20
				}
				if limit > 30 {
					limit = 30
				}
				restore, waitErr := m.beginSubagentWait(parent.StateMachine)
				if waitErr != nil {
					return taskFailure(waitErr.Error()), nil
				}
				defer restore()
				waitCtx, cancel := context.WithTimeout(ctx, time.Duration(limit)*time.Second)
				defer cancel()
				tick := time.NewTicker(100 * time.Millisecond)
				defer tick.Stop()
				for delegation.Active(session.Result.Status) {
					select {
					case <-waitCtx.Done():
						return controlResult(session.Result), nil
					case <-tick.C:
					}
					session, err = store.LoadSession(waitCtx, parent.ThreadID, session.ID)
					if err != nil {
						break
					}
				}
			default:
				err = errors.New("unknown subagent action")
			}
			if err != nil {
				return taskFailure(err.Error()), nil
			}
			return controlResult(session.Result), nil
		}}
}
func controlResult(value any) *tool.Result {
	raw, err := json.Marshal(value)
	if err != nil {
		return taskFailure(err.Error())
	}
	return &tool.Result{Content: string(raw)}
}
