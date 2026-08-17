package runtime

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// RunPhase is the durable lifecycle phase of one run. Wire events are
// projections; these phases define which work may happen next.
type RunPhase string

const (
	RunPending         RunPhase = "pending"
	RunRunning         RunPhase = "running"
	RunWaitingTool     RunPhase = "waiting_tool"
	RunWaitingApproval RunPhase = "waiting_approval"
	RunWaitingUser     RunPhase = "waiting_user"
	RunWaitingSubagent RunPhase = "waiting_subagent"
	RunCompacting      RunPhase = "compacting"
	RunCompleted       RunPhase = "completed"
	RunFailed          RunPhase = "failed"
	RunCancelled       RunPhase = "cancelled"
	RunInterrupted     RunPhase = "interrupted"
)

func (p RunPhase) Terminal() bool {
	return p == RunCompleted || p == RunFailed || p == RunCancelled || p == RunInterrupted
}

var (
	ErrInvalidRunTransition = errors.New("runtime: invalid run state transition")
	ErrRunAlreadyTerminal   = errors.New("runtime: run is already terminal")
)

// RunSnapshot is a defensive, durable view of the state machine.
type RunSnapshot struct {
	RunID      string    `json:"run_id"`
	ThreadID   string    `json:"thread_id,omitempty"`
	Phase      RunPhase  `json:"phase"`
	Version    uint64    `json:"version"`
	ApprovalID string    `json:"approval_id,omitempty"`
	QuestionID string    `json:"question_id,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	Error      string    `json:"error,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// RunStateMachine serializes lifecycle transitions. Callers can persist the
// returned snapshot after each transition without holding an internal lock.
type RunStateMachine struct {
	mu       sync.Mutex
	state    RunSnapshot
	observer func(RunSnapshot) error
}

func NewRunStateMachine(runID, threadID string) (*RunStateMachine, error) {
	if runID == "" {
		return nil, errors.New("runtime: run id must not be empty")
	}
	return &RunStateMachine{state: RunSnapshot{
		RunID: runID, ThreadID: threadID, Phase: RunPending, UpdatedAt: time.Now().UTC(),
	}}, nil
}

// RestoreRunStateMachine recreates the lifecycle authority from a canonical
// snapshot. Observers are intentionally not restored and must be attached by
// the new process before it performs another transition.
func RestoreRunStateMachine(snapshot RunSnapshot) (*RunStateMachine, error) {
	if snapshot.RunID == "" {
		return nil, errors.New("runtime: restored run id must not be empty")
	}
	if !validRunPhase(snapshot.Phase) {
		return nil, fmt.Errorf("runtime: invalid restored run phase %q", snapshot.Phase)
	}
	if snapshot.UpdatedAt.IsZero() {
		snapshot.UpdatedAt = time.Now().UTC()
	}
	return &RunStateMachine{state: snapshot}, nil
}

// SetObserver attaches the canonical event sink used by the Runtime. The
// observer persists the candidate snapshot before it becomes visible as the
// current state. It must not call back into this state machine.
func (m *RunStateMachine) SetObserver(observer func(RunSnapshot) error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.observer = observer
	m.mu.Unlock()
}

func (m *RunStateMachine) Snapshot() RunSnapshot {
	if m == nil {
		return RunSnapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *RunStateMachine) Start() error {
	return m.transition(RunRunning, func(s *RunSnapshot) {
		s.ApprovalID, s.QuestionID, s.Reason, s.Error = "", "", "", ""
	})
}

func (m *RunStateMachine) WaitApproval(approvalID string) error {
	if approvalID == "" {
		return errors.New("runtime: approval id must not be empty")
	}
	if current := m.Snapshot(); current.Phase == RunWaitingApproval && current.ApprovalID == approvalID {
		return nil
	}
	return m.transition(RunWaitingApproval, func(s *RunSnapshot) {
		s.ApprovalID, s.Reason, s.Error = approvalID, "", ""
	})
}

func (m *RunStateMachine) WaitUser(questionID string) error {
	if questionID == "" {
		return errors.New("runtime: question id must not be empty")
	}
	if current := m.Snapshot(); current.Phase == RunWaitingUser && current.QuestionID == questionID {
		return nil
	}
	return m.transition(RunWaitingUser, func(s *RunSnapshot) {
		s.QuestionID, s.ApprovalID, s.Reason, s.Error = questionID, "", "", ""
	})
}

func (m *RunStateMachine) WaitTool() error {
	if current := m.Snapshot(); current.Phase == RunWaitingTool {
		return nil
	}
	return m.transition(RunWaitingTool, func(s *RunSnapshot) { s.ApprovalID, s.QuestionID = "", "" })
}

func (m *RunStateMachine) WaitSubagent() error {
	if current := m.Snapshot(); current.Phase == RunWaitingSubagent {
		return nil
	}
	return m.transition(RunWaitingSubagent, func(s *RunSnapshot) { s.ApprovalID, s.QuestionID = "", "" })
}

func (m *RunStateMachine) BeginCompaction() error {
	return m.transition(RunCompacting, func(s *RunSnapshot) { s.ApprovalID, s.QuestionID = "", "" })
}

func (m *RunStateMachine) Resume() error {
	return m.transition(RunRunning, func(s *RunSnapshot) { s.ApprovalID, s.QuestionID = "", "" })
}

func (m *RunStateMachine) Complete(reason string) error {
	return m.transition(RunCompleted, func(s *RunSnapshot) {
		s.Reason, s.Error, s.ApprovalID, s.QuestionID = reason, "", "", ""
	})
}

func (m *RunStateMachine) Fail(err error) error {
	return m.transition(RunFailed, func(s *RunSnapshot) {
		s.Error, s.Reason, s.ApprovalID, s.QuestionID = errorText(err), "", "", ""
	})
}

func (m *RunStateMachine) Cancel(reason string) error {
	return m.transition(RunCancelled, func(s *RunSnapshot) {
		s.Reason, s.Error, s.ApprovalID, s.QuestionID = reason, "", "", ""
	})
}

func (m *RunStateMachine) Interrupt(reason string) error {
	return m.transition(RunInterrupted, func(s *RunSnapshot) {
		s.Reason, s.Error, s.ApprovalID, s.QuestionID = reason, "", "", ""
	})
}

func (m *RunStateMachine) transition(next RunPhase, mutate func(*RunSnapshot)) error {
	if m == nil {
		return errors.New("runtime: nil run state machine")
	}
	m.mu.Lock()
	current := m.state.Phase
	if current.Terminal() {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s -> %s", ErrRunAlreadyTerminal, current, next)
	}
	if !validRunTransition(current, next) {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s -> %s", ErrInvalidRunTransition, current, next)
	}
	snapshot := m.state
	snapshot.Phase = next
	snapshot.Version++
	snapshot.UpdatedAt = time.Now().UTC()
	mutate(&snapshot)
	observer := m.observer
	if observer != nil {
		if err := observer(snapshot); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	m.state = snapshot
	m.mu.Unlock()
	return nil
}

func validRunTransition(from, to RunPhase) bool {
	switch from {
	case RunPending:
		return to == RunRunning || to == RunCancelled || to == RunFailed || to == RunInterrupted
	case RunRunning:
		return to == RunWaitingTool || to == RunWaitingApproval || to == RunWaitingUser || to == RunWaitingSubagent || to == RunCompacting || to == RunCompleted || to == RunFailed || to == RunCancelled || to == RunInterrupted
	case RunWaitingTool:
		return to == RunRunning || to == RunWaitingApproval || to == RunWaitingUser || to == RunWaitingSubagent || to == RunFailed || to == RunCancelled || to == RunInterrupted
	case RunWaitingApproval:
		return to == RunRunning || to == RunWaitingTool || to == RunFailed || to == RunCancelled || to == RunInterrupted
	case RunWaitingUser:
		return to == RunRunning || to == RunWaitingTool || to == RunFailed || to == RunCancelled || to == RunInterrupted
	case RunWaitingSubagent:
		return to == RunRunning || to == RunWaitingTool || to == RunFailed || to == RunCancelled || to == RunInterrupted
	case RunCompacting:
		return to == RunRunning || to == RunFailed || to == RunCancelled || to == RunInterrupted
	default:
		return false
	}
}

func validRunPhase(phase RunPhase) bool {
	switch phase {
	case RunPending, RunRunning, RunWaitingTool, RunWaitingApproval, RunWaitingUser, RunWaitingSubagent, RunCompacting, RunCompleted, RunFailed, RunCancelled, RunInterrupted:
		return true
	default:
		return false
	}
}

func errorText(err error) string {
	if err == nil {
		return "run failed"
	}
	return err.Error()
}
