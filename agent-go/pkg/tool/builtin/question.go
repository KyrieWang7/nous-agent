package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func AskClarification(questionCapability string) tool.Definition {
	return tool.Definition{
		Name: "ask_clarification", Group: "interaction",
		Description: "Ask one focused question and wait for the user's answer in this turn. Supply options when there are clear alternatives, or omit options for a free-text question.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"},"header":{"type":"string"},"detail":{"type":"string"},"options":{"type":"array","maxItems":8,"items":{"type":"object","properties":{"label":{"type":"string"},"description":{"type":"string"}},"required":["label"]}}},"required":["question"]}`),
		Metadata:    tool.Metadata{IsReadOnly: true, IsAgentState: true},
		Handler: func(ctx context.Context, call tool.Call) (*tool.Result, error) {
			run, ok := runtime.RunContextFrom(ctx)
			if !ok || run.AgentDepth != 0 {
				return errResult(errors.New("only the main agent can ask the user directly")), nil
			}
			var args struct {
				Question string                   `json:"question"`
				Header   string                   `json:"header"`
				Detail   string                   `json:"detail"`
				Options  []runtime.QuestionOption `json:"options"`
			}
			if err := decode(call.Args, &args); err != nil {
				return errResult(err), nil
			}
			args.Question = strings.TrimSpace(args.Question)
			args.Header = strings.TrimSpace(args.Header)
			if args.Question == "" || len(args.Options) > 8 {
				return errResult(errors.New("a non-empty question and at most eight options are required")), nil
			}
			if args.Header == "" {
				args.Header = args.Question
			}
			id := "clarification:" + planReviewID(run.RunID, call.ID, args.Question)
			question, err := askUser(ctx, run, questionCapability, runtime.QuestionRequest{
				ID: id, RunID: run.RunID, ThreadID: run.ThreadID, ToolCallID: call.ID,
				Header: args.Header, Question: args.Question, Detail: args.Detail, Options: args.Options,
				Intent: runtime.QuestionIntentClarification,
			})
			if err != nil {
				return nil, err
			}
			if question.Status != runtime.QuestionAnswered {
				return &tool.Result{Content: "The user skipped the question. Continue only with information already available."}, nil
			}
			answer, err := json.Marshal(question.Answer)
			if err != nil {
				return nil, err
			}
			return &tool.Result{Content: "User answer: " + string(answer)}, nil
		},
	}
}

func askUser(ctx context.Context, run runtime.RunContext, name string, request runtime.QuestionRequest) (runtime.QuestionRequest, error) {
	if run.StateMachine == nil || run.Publish == nil {
		return runtime.QuestionRequest{}, errors.New("user questions require a run state and event publisher")
	}
	manager, err := capability.ResolveViewAs[*runtime.QuestionManager](ctx, run.Capabilities, name, capability.KindInteraction, capability.ResolveRequest{
		GenerationID: run.GenerationID, ThreadID: run.ThreadID, RunID: run.RunID, Values: run.Values,
	})
	if err != nil {
		return runtime.QuestionRequest{}, fmt.Errorf("resolving user questions: %w", err)
	}
	question, err := manager.Create(ctx, request)
	if errors.Is(err, runtime.ErrQuestionExists) {
		question, err = manager.Get(ctx, request.ID)
	}
	if err != nil || question.Status != runtime.QuestionPending {
		return question, err
	}
	if err = run.StateMachine.WaitUser(question.ID); err != nil {
		return question, err
	}
	if err = publishQuestion(ctx, run, runtime.EventQuestionRequested, question, question.ID+":requested"); err != nil {
		return question, err
	}
	question, err = manager.Wait(ctx, request.ID, 100*time.Millisecond)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		settled, dismissErr := manager.Dismiss(cleanup, request.ID, "system")
		if dismissErr == nil {
			dismissErr = publishQuestion(cleanup, run, runtime.EventQuestionResolved, settled, request.ID+":resolved")
		}
		return question, errors.Join(err, dismissErr)
	}
	if !run.StateMachine.Snapshot().Phase.Terminal() {
		if err := run.StateMachine.WaitTool(); err != nil {
			return question, err
		}
	}
	return question, publishQuestion(context.WithoutCancel(ctx), run, runtime.EventQuestionResolved, question, question.ID+":resolved")
}
