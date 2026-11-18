// Package inbox defines durable root-session input admission independently of
// the HTTP boundary, kernel and storage drivers.
package inbox

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
)

var (
	ErrNotFound    = errors.New("inbox message not found")
	ErrConflict    = errors.New("message ID already belongs to different input")
	ErrAdmitted    = errors.New("message is being prepared or has been admitted; stop the current run instead")
	ErrUnavailable = errors.New("durable session inbox unavailable")
	ErrChanged     = errors.New("the queued message changed; refresh before editing")
	ErrNoActiveRun = errors.New("the current run changed before the message could be sent")
	ErrTextOnly    = errors.New("the queued message contains attachments and cannot be edited as text")
)

type Delivery string

const (
	NextTurn Delivery = "next_turn"
	NextStep Delivery = "next_step"
)

type Input struct {
	Prompt        string                 `json:"prompt"`
	ContentBlocks []message.ContentBlock `json:"content_blocks,omitempty"`
	Config        map[string]any         `json:"config,omitempty"`
	Context       map[string]any         `json:"context,omitempty"`
}
type Entry struct {
	ID           string     `json:"id"`
	ThreadID     string     `json:"thread_id"`
	Delivery     Delivery   `json:"delivery"`
	Input        Input      `json:"input"`
	Status       string     `json:"status"`
	Position     int64      `json:"position"`
	RunID        string     `json:"run_id,omitempty"`
	TargetRunID  string     `json:"target_run_id,omitempty"`
	RunStatus    string     `json:"run_status,omitempty"`
	RunCreatedAt *time.Time `json:"run_created_at,omitempty"`
	Error        string     `json:"error,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	AdmittedAt   *time.Time `json:"admitted_at,omitempty"`
}

type Change struct {
	Prompt      *string
	TargetRunID string
	UpdatedAt   time.Time
}

func (e Entry) Validate() error {
	if strings.TrimSpace(e.ID) == "" || len(e.ID) > 200 || strings.ContainsRune(e.ID, 0) {
		return errors.New("a message ID of 1-200 characters is required")
	}
	if e.Delivery != NextTurn && e.Delivery != NextStep {
		return errors.New("delivery must be next_turn or next_step")
	}
	if strings.TrimSpace(e.Input.Prompt) == "" && len(e.Input.ContentBlocks) == 0 {
		return errors.New("message content is required")
	}
	if e.Delivery == NextStep && (len(e.Input.Config) > 0 || len(e.Input.Context) > 0) {
		return errors.New("next_step guidance cannot change run configuration")
	}
	return nil
}

// Store atomically creates the queued run and claims its initial input. Claim
// is called only while the service owns the thread's exclusive execution lease.
// Admit commits the canonical transcript and ACKs together, fenced by run ID.
type Store interface {
	Enqueue(context.Context, Entry) (Entry, error)
	ListInputs(context.Context, string, int64, int) ([]Entry, error)
	GetInput(context.Context, string, string) (Entry, error)
	CancelInput(context.Context, string, string) (Entry, error)
	ChangeInput(context.Context, string, string, Change) (Entry, error)
	PendingThreads(context.Context, int) ([]string, error)
	HasPending(context.Context, string) (bool, error)
	Claim(context.Context, string, string) (Entry, bool, error)
	Steering(context.Context, string, string) ([]Entry, error)
	Admit(context.Context, string, string, []string, []message.Message) error
	Finish(context.Context, string, string, string, bool) error
}

// Source returns proposed messages and an ACK bound to the prepared request.
// It must not remove messages or append to canonical history before ACK.
type Source func(context.Context) ([]message.Message, func(context.Context, []message.Message) error, error)
