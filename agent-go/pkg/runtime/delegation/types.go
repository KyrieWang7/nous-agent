// Package delegation defines durable child-session contracts independently of
// the kernel, transports and storage drivers.
package delegation

import (
	"context"
	"errors"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

var (
	ErrMessageConflict = errors.New("message id already belongs to different content")
	ErrNotFound        = errors.New("subagent session not found")
	ErrBusy            = errors.New("subagent session is running")
	ErrLeaseLost       = errors.New("subagent session lease lost")
)

type Result struct {
	Description       string      `json:"description,omitempty"`
	SessionID         string      `json:"session_id,omitempty"`
	ParentRunID       string      `json:"parent_run_id,omitempty"`
	ThreadID          string      `json:"thread_id,omitempty"`
	RunID             string      `json:"run_id,omitempty"`
	Provider          string      `json:"provider,omitempty"`
	StopReason        string      `json:"stop_reason,omitempty"`
	TaskID            string      `json:"task_id"`
	SubagentType      string      `json:"subagent_type"`
	Status            string      `json:"status"`
	Output            string      `json:"output,omitempty"`
	Error             string      `json:"error,omitempty"`
	CoordinationError string      `json:"coordination_error,omitempty"`
	Usage             model.Usage `json:"usage"`
	StartedAt         time.Time   `json:"started_at"`
	CompletedAt       *time.Time  `json:"completed_at,omitempty"`
}

// Request is a trusted application command. It is distinct from arbitrary run
// config/context maps and cannot be smuggled through a model option.
type Request struct {
	SessionID string `json:"session_id"`
	Prompt    string `json:"prompt"`
}

type Session struct {
	ID              string            `json:"id"`
	ThreadID        string            `json:"thread_id"`
	ParentSessionID string            `json:"parent_session_id,omitempty"`
	Result          Result            `json:"result"`
	History         []message.Message `json:"history"`
	State           map[string]any    `json:"state,omitempty"`
	AllowedTools    []string          `json:"allowed_tools"`
	MaxTurns        int               `json:"max_turns"`
	SystemPrompt    string            `json:"system_prompt,omitempty"`
	Token           string            `json:"-"`
	LeaseUntil      time.Time         `json:"lease_until"`
	CancelRequested bool              `json:"cancel_requested"`
}

// SessionStore fences every write by Token. Takeover is allowed only for an
// idle or expired session. Interrupted executions are never blindly replayed.
type SessionStore interface {
	Acquire(context.Context, Session, time.Duration) (Session, error)
	Checkpoint(context.Context, Session) error
	Heartbeat(context.Context, string, string, time.Duration) (bool, error)
	LoadSession(context.Context, string, string) (Session, error)
	ListSessions(context.Context, string) ([]Session, error)
	CancelSession(context.Context, string, string) error
}

type TaskStore interface {
	Put(context.Context, Result, time.Duration) error
	Get(context.Context, string) (Result, error)
}

type ScopedTaskStore interface {
	GetScoped(context.Context, string, string, string) (Result, error)
}

type InboxMessage struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}
type InboxStore interface {
	Enqueue(context.Context, string, string, InboxMessage) (InboxMessage, error)
	Pending(context.Context, string, string) ([]InboxMessage, error)
	CommitInputs(context.Context, Session, []string) error
	ListInputs(context.Context, string, string) ([]InboxMessage, error)
}

func Active(status string) bool { return status == "pending" || status == "running" }
