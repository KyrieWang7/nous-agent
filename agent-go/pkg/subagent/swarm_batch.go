package subagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

const maxReviewContextChars = 48_000

type swarmBatchItem struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Prompt        string   `json:"prompt"`
	SubagentType  string   `json:"subagent_type"`
	MaxTurns      int      `json:"max_turns,omitempty"`
	RestrictTools []string `json:"restrict_tools,omitempty"`
}

type swarmBatchReview struct {
	Enabled      bool   `json:"enabled"`
	Name         string `json:"name,omitempty"`
	SubagentType string `json:"subagent_type,omitempty"`
	Prompt       string `json:"prompt,omitempty"`
}

type swarmBatchArgs struct {
	Objective string           `json:"objective"`
	Items     []swarmBatchItem `json:"items"`
	Review    swarmBatchReview `json:"review,omitempty"`
}

type swarmBatchWorkerResult struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Result Result `json:"result"`
}

type swarmBatchResult struct {
	Objective string                   `json:"objective"`
	Status    string                   `json:"status"`
	Workers   []swarmBatchWorkerResult `json:"workers"`
	Review    *Result                  `json:"review,omitempty"`
}

// SwarmBatchTool provides a deterministic batch contract above the ordinary
// task dispatcher. Every worker remains an independent child run with the same
// budgets, policy, lifecycle, persistence, and progress events as task.
func (m *Manager) SwarmBatchTool() tool.Definition {
	parameters := json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"objective":{"type":"string"},"items":{"type":"array","minItems":2,"maxItems":%d,"items":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},"description":{"type":"string"},"prompt":{"type":"string"},"subagent_type":{"type":"string"},"max_turns":{"type":"integer","minimum":1},"restrict_tools":{"type":"array","items":{"type":"string"}}},"required":["id","name","description","prompt","subagent_type"]}},"review":{"type":"object","properties":{"enabled":{"type":"boolean"},"name":{"type":"string"},"subagent_type":{"type":"string"},"prompt":{"type":"string"}}}},"required":["objective","items"]}`, cap(m.sem)))
	return tool.Definition{
		Name:        "swarm_batch",
		Group:       "swarm",
		Description: "Dispatch 2 or more independent swarm workers concurrently, optionally review their results, and return an ordered batch for lead synthesis.",
		Parameters:  parameters,
		Metadata:    tool.Metadata{IsAgentState: true},
		Handler: func(ctx context.Context, call tool.Call) (*tool.Result, error) {
			args, err := decodeSwarmBatchArgs(call.Args)
			if err != nil {
				return swarmBatchFailure(err), nil
			}
			if err := m.validateSwarmBatch(args); err != nil {
				return swarmBatchFailure(err), nil
			}
			result := m.dispatchSwarmBatch(ctx, call.ID, args)
			raw, err := json.Marshal(result)
			if err != nil {
				return nil, fmt.Errorf("subagent: encoding swarm batch result: %w", err)
			}
			return &tool.Result{Content: "Swarm batch result: " + string(raw)}, nil
		},
	}
}

func decodeSwarmBatchArgs(raw []byte) (swarmBatchArgs, error) {
	var args swarmBatchArgs
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return swarmBatchArgs{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("swarm_batch arguments must contain one JSON value")
		}
		return swarmBatchArgs{}, err
	}
	return args, nil
}

func (m *Manager) validateSwarmBatch(args swarmBatchArgs) error {
	if strings.TrimSpace(args.Objective) == "" {
		return errors.New("subagent: swarm_batch objective is required")
	}
	limit := cap(m.sem)
	if len(args.Items) < 2 || len(args.Items) > limit {
		return fmt.Errorf("subagent: swarm_batch requires 2-%d items", limit)
	}
	ids := make(map[string]struct{}, len(args.Items))
	names := make(map[string]struct{}, len(args.Items)+1)
	for i, item := range args.Items {
		item.ID = strings.TrimSpace(item.ID)
		item.Name = strings.TrimSpace(item.Name)
		if item.ID == "" || item.Name == "" || strings.TrimSpace(item.Description) == "" || strings.TrimSpace(item.Prompt) == "" || strings.TrimSpace(item.SubagentType) == "" {
			return fmt.Errorf("subagent: swarm_batch item %d requires id, name, description, prompt, and subagent_type", i)
		}
		if _, exists := ids[item.ID]; exists {
			return fmt.Errorf("subagent: duplicate swarm_batch item id %q", item.ID)
		}
		if _, exists := names[item.Name]; exists {
			return fmt.Errorf("subagent: duplicate swarm_batch worker name %q", item.Name)
		}
		ids[item.ID] = struct{}{}
		names[item.Name] = struct{}{}
		m.mu.RLock()
		_, registered := m.defs[strings.TrimSpace(item.SubagentType)]
		m.mu.RUnlock()
		if !registered {
			return fmt.Errorf("subagent: unknown subagent type %q", item.SubagentType)
		}
	}
	if args.Review.Enabled {
		name := strings.TrimSpace(args.Review.Name)
		if name == "" {
			name = "reviewer"
		}
		if _, exists := names[name]; exists {
			return fmt.Errorf("subagent: reviewer name %q conflicts with a worker", name)
		}
		profile := strings.TrimSpace(args.Review.SubagentType)
		if profile == "" {
			profile = "verification"
		}
		m.mu.RLock()
		_, registered := m.defs[profile]
		m.mu.RUnlock()
		if !registered {
			return fmt.Errorf("subagent: unknown reviewer subagent type %q", profile)
		}
	}
	return nil
}

func (m *Manager) dispatchSwarmBatch(ctx context.Context, callID string, args swarmBatchArgs) swarmBatchResult {
	workers := make([]swarmBatchWorkerResult, len(args.Items))
	var wg sync.WaitGroup
	for index, item := range args.Items {
		index, item := index, item
		wg.Add(1)
		go func() {
			defer wg.Done()
			request := DispatchRequest{
				SubagentType: strings.TrimSpace(item.SubagentType), Description: strings.TrimSpace(item.Description),
				Name: strings.TrimSpace(item.Name), MaxTurns: item.MaxTurns, Prompt: strings.TrimSpace(item.Prompt),
				RestrictTools: item.RestrictTools, ToolCallID: batchTaskID(callID, item.ID, index),
			}
			result, err := m.Dispatch(ctx, request)
			if err != nil && result.TaskID == "" {
				result = Result{TaskID: request.ToolCallID, SubagentType: request.SubagentType, Status: message.SubagentFailed, Error: err.Error()}
			}
			workers[index] = swarmBatchWorkerResult{ID: strings.TrimSpace(item.ID), Name: request.Name, Result: result}
		}()
	}
	wg.Wait()

	batch := swarmBatchResult{Objective: strings.TrimSpace(args.Objective), Workers: workers}
	batch.Status = swarmBatchStatus(workers)
	if args.Review.Enabled {
		review := m.dispatchSwarmReview(ctx, callID, args, workers)
		batch.Review = &review
		if review.Status != message.SubagentCompleted && batch.Status == message.SubagentCompleted {
			batch.Status = "partial"
		}
	}
	return batch
}

func (m *Manager) dispatchSwarmReview(ctx context.Context, callID string, args swarmBatchArgs, workers []swarmBatchWorkerResult) Result {
	name := strings.TrimSpace(args.Review.Name)
	if name == "" {
		name = "reviewer"
	}
	profile := strings.TrimSpace(args.Review.SubagentType)
	if profile == "" {
		profile = "verification"
	}
	prompt := strings.TrimSpace(args.Review.Prompt)
	if prompt == "" {
		prompt = "Check completeness, contradictions, unsupported claims, and material gaps. Return a concise verdict and specific corrections for the lead."
	}
	payload, _ := json.Marshal(struct {
		Objective string                   `json:"objective"`
		Workers   []swarmBatchWorkerResult `json:"workers"`
	}{Objective: strings.TrimSpace(args.Objective), Workers: workers})
	reviewPrompt := "You are the reviewer for a swarm batch. " + prompt + "\n\nWorker results:\n" + truncateBatchContext(string(payload), maxReviewContextChars)
	result, err := m.Dispatch(ctx, DispatchRequest{
		SubagentType: profile, Description: "Review swarm worker results", Name: name,
		Prompt: reviewPrompt, ToolCallID: batchTaskID(callID, "review", len(workers)),
	})
	if err != nil && result.TaskID == "" {
		result = Result{TaskID: batchTaskID(callID, "review", len(workers)), SubagentType: profile, Status: message.SubagentFailed, Error: err.Error()}
	}
	return result
}

func swarmBatchStatus(workers []swarmBatchWorkerResult) string {
	completed := 0
	for _, worker := range workers {
		if worker.Result.Status == message.SubagentCompleted {
			completed++
		}
	}
	switch {
	case completed == len(workers):
		return message.SubagentCompleted
	case completed == 0:
		return message.SubagentFailed
	default:
		return "partial"
	}
}

func batchTaskID(callID, itemID string, index int) string {
	prefix := strings.TrimSpace(callID)
	if prefix == "" {
		prefix = newTaskID()
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		itemID = fmt.Sprintf("item-%d", index+1)
	}
	return prefix + ":" + itemID
}

func truncateBatchContext(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "\n[truncated]"
}

func swarmBatchFailure(err error) *tool.Result {
	return &tool.Result{Content: "Swarm batch failed. Error: " + err.Error(), IsError: true}
}
