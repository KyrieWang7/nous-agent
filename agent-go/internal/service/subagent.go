package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/delegation"
)

var ErrSubagentsUnavailable = errors.New("persistent subagent service unavailable")

// SessionView deliberately excludes the persisted system prompt and lease token.
type SessionView struct {
	ID              string            `json:"id"`
	ParentSessionID string            `json:"parent_session_id,omitempty"`
	Result          delegation.Result `json:"result"`
	History         []message.Message `json:"history,omitempty"`
	CancelRequested bool              `json:"cancel_requested"`
}

func sessionView(s delegation.Session) SessionView {
	return SessionView{ID: s.ID, ParentSessionID: s.ParentSessionID, Result: s.Result, History: s.History, CancelRequested: s.CancelRequested}
}

func (s *Service) SendSubagentInput(ctx context.Context, tid, id string, input delegation.InboxMessage) (delegation.InboxMessage, error) {
	if _, err := s.GetSubagent(ctx, tid, id); err != nil {
		return input, err
	}
	store, ok := s.subagents.(delegation.InboxStore)
	if !ok {
		return input, ErrSubagentsUnavailable
	}
	return store.Enqueue(ctx, tid, id, input)
}
func (s *Service) SubagentInputs(ctx context.Context, tid, id string) ([]delegation.InboxMessage, error) {
	if _, err := s.GetSubagent(ctx, tid, id); err != nil {
		return nil, err
	}
	store, ok := s.subagents.(delegation.InboxStore)
	if !ok {
		return nil, ErrSubagentsUnavailable
	}
	return store.ListInputs(ctx, tid, id)
}
func (s *Service) ListSubagents(ctx context.Context, tid string) ([]SessionView, error) {
	if _, err := s.store.GetThread(ctx, tid); err != nil {
		return nil, err
	}
	if s.subagents == nil {
		return nil, ErrSubagentsUnavailable
	}
	sessions, err := s.subagents.ListSessions(ctx, tid)
	if err != nil {
		return nil, err
	}
	out := make([]SessionView, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, sessionView(session))
	}
	return out, nil
}
func (s *Service) GetSubagent(ctx context.Context, tid, id string) (SessionView, error) {
	if _, err := s.store.GetThread(ctx, tid); err != nil {
		return SessionView{}, err
	}
	if s.subagents == nil {
		return SessionView{}, ErrSubagentsUnavailable
	}
	session, err := s.subagents.LoadSession(ctx, tid, id)
	return sessionView(session), err
}
func (s *Service) CancelSubagent(ctx context.Context, tid, id string) (SessionView, error) {
	view, err := s.GetSubagent(ctx, tid, id)
	if err != nil {
		return view, err
	}
	if !delegation.Active(view.Result.Status) {
		return view, nil
	}
	if err = s.subagents.CancelSession(ctx, tid, id); err != nil {
		return view, err
	}
	return s.GetSubagent(ctx, tid, id)
}
func (s *Service) WaitSubagent(ctx context.Context, tid, id string) (SessionView, error) {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		view, err := s.GetSubagent(ctx, tid, id)
		if err != nil || !delegation.Active(view.Result.Status) {
			return view, err
		}
		select {
		case <-ctx.Done():
			return view, ctx.Err()
		case <-tick.C:
		}
	}
}
func (s *Service) ContinueSubagent(ctx context.Context, tid, id, prompt string) (model.Run, error) {
	view, err := s.GetSubagent(ctx, tid, id)
	if err != nil {
		return model.Run{}, err
	}
	if view.ParentSessionID != "" {
		return model.Run{}, delegation.ErrNotFound
	}
	if delegation.Active(view.Result.Status) {
		return model.Run{}, delegation.ErrBusy
	}
	if strings.TrimSpace(prompt) == "" {
		return model.Run{}, errors.New("prompt is required")
	}
	return s.StartRun(ctx, tid, RunInput{AssistantID: "lead_agent", OnDisconnect: "continue", Prompt: prompt, Delegation: &delegation.Request{SessionID: id, Prompt: prompt}, Metadata: map[string]any{"subagent_session_id": id}})
}
