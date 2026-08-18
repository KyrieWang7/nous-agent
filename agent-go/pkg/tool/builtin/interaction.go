package builtin

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

const maxImageBytes = 20 << 20

const outputDirectory = "/mnt/user-data/outputs"

const (
	planApproveLabel      = "Approve"
	planKeepPlanningLabel = "Keep planning"
)

func WriteTodos() tool.Definition {
	return tool.Definition{
		Name:        "write_todos",
		Group:       "planning",
		Description: "Create or replace the current structured task list. In planning, define the execution steps; during execution, update each step immediately as its status changes.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"todos":{"type":"array","items":{"type":"object","properties":{"content":{"type":"string"},"status":{"type":"string","enum":["pending","in_progress","completed"]}},"required":["content","status"]}}},"required":["todos"]}`),
		Metadata:    tool.Metadata{IsAgentState: true},
		Handler: func(ctx context.Context, call tool.Call) (*tool.Result, error) {
			run, ok := runtime.RunContextFrom(ctx)
			mode := runtime.Mode(strings.ToLower(strings.TrimSpace(fmt.Sprint(run.Values["mode"]))))
			if !ok || (mode != runtime.ModePro && mode != runtime.ModeUltra) {
				return errResult(fmt.Errorf("write_todos is available only in pro or ultra mode")), nil
			}
			var args struct {
				Todos []struct {
					Content string `json:"content"`
					Status  string `json:"status"`
				} `json:"todos"`
			}
			if err := json.Unmarshal(call.Args, &args); err != nil {
				return errResult(err), nil
			}
			if len(args.Todos) == 0 {
				return errResult(errors.New("write_todos requires at least one task")), nil
			}
			planning, _ := run.Values["is_plan_mode"].(bool)
			inProgress := 0
			todos := make([]map[string]string, 0, len(args.Todos))
			for _, item := range args.Todos {
				content := strings.TrimSpace(item.Content)
				if content == "" {
					return errResult(errors.New("write_todos task content cannot be empty")), nil
				}
				switch item.Status {
				case "pending", "in_progress", "completed":
				default:
					return errResult(fmt.Errorf("write_todos invalid task status %q", item.Status)), nil
				}
				if planning && item.Status != "pending" {
					return errResult(errors.New("write_todos requires every task to be pending until the plan is approved")), nil
				}
				if item.Status == "in_progress" {
					inProgress++
				}
				todos = append(todos, map[string]string{"content": content, "status": item.Status})
			}
			if inProgress > 1 {
				return errResult(errors.New("write_todos allows at most one task to be in_progress")), nil
			}
			run.Values["todos"] = todos
			if run.PersistValues == nil {
				return nil, errors.New("write_todos: thread state persistence is unavailable")
			}
			if err := run.PersistValues(ctx, run.Values); err != nil {
				return nil, fmt.Errorf("write_todos: %w", err)
			}
			raw, _ := json.Marshal(todos)
			return &tool.Result{Content: "Task list updated: " + string(raw)}, nil
		},
	}
}

func ExitPlanMode(questionCapability string) tool.Definition {
	return tool.Definition{
		Name:  "exit_plan_mode",
		Group: "planning",
		Description: "Use only in plan mode. Present the complete plan for user review and leave plan mode only after approval. " +
			"The plan must be Markdown starting with a # heading. If the user keeps planning, revise it using their feedback and present it again.",
		Parameters: json.RawMessage(`{"type":"object","properties":{"plan":{"type":"string","description":"The complete Markdown plan, starting with a # heading."}},"required":["plan"]}`),
		Metadata:   tool.Metadata{IsAgentState: true},
		Handler: func(ctx context.Context, call tool.Call) (*tool.Result, error) {
			run, ok := runtime.RunContextFrom(ctx)
			active, _ := run.Values["is_plan_mode"].(bool)
			if !ok || !active {
				return errResult(fmt.Errorf("exit_plan_mode is available only while plan mode is active")), nil
			}
			if !hasTodos(run.Values["todos"]) {
				return errResult(errors.New("exit_plan_mode requires a non-empty task list; call write_todos first")), nil
			}
			if !hasIncompleteTodo(run.Values["todos"]) {
				return errResult(errors.New("exit_plan_mode requires at least one task that remains to be executed")), nil
			}
			var args struct {
				Plan string `json:"plan"`
			}
			if err := json.Unmarshal(call.Args, &args); err != nil {
				return errResult(err), nil
			}
			args.Plan = strings.TrimSpace(args.Plan)
			if !strings.HasPrefix(args.Plan, "# ") || strings.TrimSpace(strings.TrimPrefix(strings.SplitN(args.Plan, "\n", 2)[0], "# ")) == "" {
				return errResult(fmt.Errorf("exit_plan_mode requires a non-empty Markdown plan starting with a # heading")), nil
			}
			if !run.Capabilities.Initialized() {
				return nil, errors.New("exit_plan_mode: capability view is not initialized")
			}
			questions, err := capability.ResolveViewAs[*runtime.QuestionManager](ctx, run.Capabilities, questionCapability, capability.KindInteraction, capability.ResolveRequest{
				GenerationID: run.GenerationID, ThreadID: run.ThreadID, RunID: run.RunID, Values: run.Values,
			})
			if err != nil {
				return nil, fmt.Errorf("exit_plan_mode: resolving user question capability: %w", err)
			}
			questionID := planReviewID(run.RunID, call.ID, args.Plan)
			question, err := questions.Create(ctx, runtime.QuestionRequest{
				ID: questionID, RunID: run.RunID, ThreadID: run.ThreadID, ToolCallID: call.ID,
				Header: "Review the complete plan", Question: "Approve this complete plan once and execute all todos?", Detail: args.Plan,
				Options: []runtime.QuestionOption{
					{Label: planApproveLabel, Description: "Approve the entire plan once, leave plan mode, and execute every todo."},
					{Label: planKeepPlanningLabel, Description: "Stay in plan mode and return feedback to the model."},
				},
				Intent: "plan-review",
			})
			if err != nil && !errors.Is(err, runtime.ErrQuestionExists) {
				return nil, err
			}
			if errors.Is(err, runtime.ErrQuestionExists) {
				question, err = questions.Get(ctx, questionID)
				if err != nil {
					return nil, err
				}
			}
			if question.Status == runtime.QuestionPending {
				if run.StateMachine == nil {
					return nil, errors.New("exit_plan_mode: run state machine is unavailable")
				}
				if err := run.StateMachine.WaitUser(questionID); err != nil {
					return nil, err
				}
				if err := publishQuestion(ctx, run, runtime.EventQuestionRequested, question, questionID+":requested"); err != nil {
					return nil, err
				}
				question, err = questions.Wait(ctx, questionID, 100*time.Millisecond)
				if err != nil {
					return nil, err
				}
				if !run.StateMachine.Snapshot().Phase.Terminal() {
					if err := run.StateMachine.WaitTool(); err != nil {
						return nil, err
					}
				}
				if err := publishQuestion(context.WithoutCancel(ctx), run, runtime.EventQuestionResolved, question, questionID+":resolved"); err != nil {
					return nil, err
				}
			}
			if question.Status == runtime.QuestionDismissed {
				return errResult(errors.New("the user dismissed the plan review; stay in plan mode and wait for their message")), nil
			}
			if question.Status != runtime.QuestionAnswered || len(question.Answer.Selected) != 1 || question.Answer.Selected[0] != planApproveLabel || question.Answer.Custom != "" {
				feedback := strings.TrimSpace(question.Answer.Custom)
				if feedback == "" {
					return errResult(errors.New("the user chose to keep planning; revise the plan and present it again")), nil
				}
				return errResult(fmt.Errorf("the user chose to keep planning; feedback: %s", feedback)), nil
			}
			todos, err := activateFirstPendingTodo(run.Values["todos"])
			if err != nil {
				return nil, fmt.Errorf("exit_plan_mode: activating approved plan: %w", err)
			}
			if run.PersistValues == nil {
				return nil, errors.New("exit_plan_mode: thread state persistence is unavailable")
			}
			if run.Publish == nil {
				return nil, errors.New("exit_plan_mode: durable event publisher is unavailable")
			}
			previousTodos := run.Values["todos"]
			run.Values["is_plan_mode"] = false
			run.Values["todos"] = todos
			if err := run.PersistValues(context.WithoutCancel(ctx), run.Values); err != nil {
				run.Values["is_plan_mode"] = true
				run.Values["todos"] = previousTodos
				return nil, fmt.Errorf("exit_plan_mode: persisting approved plan state: %w", err)
			}
			planEvent := runtime.MustEvent(run.EventStreamRunID(), run.ThreadID, runtime.EventPlanModeChanged, runtime.PlanModeChanged{Active: false})
			planEvent.IdempotencyKey = questionID + ":plan-mode-inactive"
			if _, err := run.Publish(context.WithoutCancel(ctx), planEvent); err != nil {
				run.Values["is_plan_mode"] = true
				run.Values["todos"] = previousTodos
				rollbackErr := run.PersistValues(context.WithoutCancel(ctx), run.Values)
				return nil, errors.Join(fmt.Errorf("exit_plan_mode: persisting plan mode: %w", err), rollbackErr)
			}
			return &tool.Result{Content: "Plan approved. Plan mode exited; carry out the plan starting with the next step."}, nil
		},
	}
}

func activateFirstPendingTodo(value any) ([]map[string]string, error) {
	var todos []map[string]string
	switch items := value.(type) {
	case []map[string]string:
		for _, item := range items {
			todos = append(todos, map[string]string{"content": item["content"], "status": item["status"]})
		}
	case []any:
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				return nil, errors.New("invalid persisted todo item")
			}
			todos = append(todos, map[string]string{"content": strings.TrimSpace(fmt.Sprint(item["content"])), "status": strings.TrimSpace(fmt.Sprint(item["status"]))})
		}
	default:
		return nil, errors.New("approved plan has no task list")
	}
	activated := false
	for _, item := range todos {
		if item["status"] != "pending" {
			return nil, fmt.Errorf("approved plan contains non-pending task %q", item["content"])
		}
		if !activated {
			item["status"] = "in_progress"
			activated = true
		}
	}
	if !activated {
		return nil, errors.New("approved plan has no pending task")
	}
	return todos, nil
}

func hasTodos(value any) bool {
	switch items := value.(type) {
	case []map[string]string:
		return len(items) > 0
	case []any:
		return len(items) > 0
	default:
		return false
	}
}

func hasIncompleteTodo(value any) bool {
	switch items := value.(type) {
	case []map[string]string:
		for _, item := range items {
			if item["status"] != "completed" {
				return true
			}
		}
	case []any:
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok || item["status"] != "completed" {
				return true
			}
		}
	}
	return false
}

func planReviewID(runID, toolCallID, plan string) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + toolCallID + "\x00" + plan))
	return "question-" + hex.EncodeToString(sum[:12])
}

func publishQuestion(ctx context.Context, run runtime.RunContext, eventType runtime.EventType, question runtime.QuestionRequest, key string) error {
	event := runtime.MustEvent(run.EventStreamRunID(), run.ThreadID, eventType, question)
	event.IdempotencyKey = key
	if run.Publish == nil {
		return errors.New("user question: durable event publisher is unavailable")
	}
	_, err := run.Publish(ctx, event)
	return err
}

func ViewImage() tool.Definition {
	return tool.Definition{
		Name:        "view_image",
		Group:       GroupFileRead,
		Description: "Read one PNG, JPEG, or WebP image from the workspace and make it visible to the model.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"image_path":{"type":"string"}},"required":["image_path"]}`),
		Metadata:    tool.Metadata{IsReadOnly: true, IsConcurrencySafe: true, RequiresSandbox: true},
		Handler: func(ctx context.Context, call tool.Call) (*tool.Result, error) {
			var args struct {
				ImagePath string `json:"image_path"`
				Path      string `json:"path"`
			}
			if err := json.Unmarshal(call.Args, &args); err != nil {
				return errResult(err), nil
			}
			if args.ImagePath != "" {
				args.Path = args.ImagePath
			}
			h, err := sandbox.HandleFromContext(ctx)
			if err != nil {
				return nil, err
			}
			mime, ok := imageMIME(filepath.Ext(args.Path))
			if !ok {
				return errResult(fmt.Errorf("view_image: unsupported format %q", filepath.Ext(args.Path))), nil
			}
			data, err := h.FS().ReadFile(ctx, args.Path)
			if err != nil {
				return errResult(err), nil
			}
			if len(data) > maxImageBytes {
				return errResult(fmt.Errorf("view_image: image is %d bytes; limit is %d", len(data), maxImageBytes)), nil
			}
			return &tool.Result{
				Content:       "Successfully read image " + args.Path,
				ContentBlocks: []message.ContentBlock{{Type: "image", MimeType: mime, Data: base64.StdEncoding.EncodeToString(data)}},
			}, nil
		},
	}
}

// PresentFiles validates final workspace artifacts and makes their virtual
// paths explicit in the tool transcript. The frontend renders file cards from
// the corresponding assistant tool call, while gateways serve the same paths
// from the shared per-thread workspace volume.
func PresentFiles() tool.Definition {
	return tool.Definition{
		Name:        "present_files",
		Group:       "interaction",
		Description: "Present final files from /mnt/user-data/outputs to the user for viewing or download.",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "filepaths": {
      "type": "array",
      "items": {"type": "string"},
      "minItems": 1,
      "description": "Absolute virtual paths under /mnt/user-data/outputs."
    }
  },
  "required": ["filepaths"]
}`),
		Metadata: tool.Metadata{IsReadOnly: true, IsConcurrencySafe: true, RequiresSandbox: true},
		Handler: func(ctx context.Context, call tool.Call) (*tool.Result, error) {
			var args struct {
				Filepaths []string `json:"filepaths"`
			}
			if err := json.Unmarshal(call.Args, &args); err != nil {
				return errResult(err), nil
			}
			handle, err := sandbox.HandleFromContext(ctx)
			if err != nil {
				return nil, err
			}
			if len(args.Filepaths) == 0 {
				return errResult(fmt.Errorf("present_files: at least one filepath is required")), nil
			}

			artifacts := make([]tool.Artifact, 0, len(args.Filepaths))
			for _, filepath := range args.Filepaths {
				clean := path.Clean(strings.TrimSpace(filepath))
				if clean == outputDirectory || !strings.HasPrefix(clean, outputDirectory+"/") {
					return errResult(fmt.Errorf("present_files: %q is outside %s", filepath, outputDirectory)), nil
				}
				entry, err := handle.FS().Stat(ctx, clean)
				if err != nil {
					return errResult(fmt.Errorf("present_files: %q: %w", clean, err)), nil
				}
				if entry.IsDir {
					return errResult(fmt.Errorf("present_files: %q is a directory", clean)), nil
				}
				artifacts = append(artifacts, tool.Artifact{Kind: "file", Ref: clean, Size: entry.Size})
			}
			raw, _ := json.Marshal(args.Filepaths)
			return &tool.Result{Content: "Successfully presented files: " + string(raw), Artifacts: artifacts}, nil
		},
	}
}

func imageMIME(ext string) (string, bool) {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".png":
		return "image/png", true
	case ".webp":
		return "image/webp", true
	default:
		return "", false
	}
}
