package service

import (
	"context"
	"errors"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/model"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/repository"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/delegation"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/runmanager"
)

var (
	ErrThreadBusy       = repository.ErrThreadBusy
	ErrClosing          = errors.New("server is shutting down")
	ErrRunNotRegistered = errors.New("run is no longer registered and cannot be cancelled")
)

// RunInput is transport-neutral admission data. Wire parsing errors are settled by RunManager.
type RunInput struct {
	Delegation       *delegation.Request
	AssistantID      string
	Metadata         map[string]any
	OnDisconnect     string
	Prompt           string
	ContentBlocks    []message.ContentBlock
	Config           map[string]any
	Context          map[string]any
	PreparationError error
}

// StartRun admits and starts a normal Runtime run. This is the only application
// execution path; HTTP, CLI and schedulers must not create another Agent Loop.
func (s *Service) StartRun(ctx context.Context, tid string, input RunInput) (model.Run, error) {
	if !s.beginRun() {
		return model.Run{}, ErrClosing
	}
	lease, err := s.store.AcquireThread(ctx, tid)
	if err != nil {
		s.runs.Done()
		return model.Run{}, err
	}
	handedOff := false
	defer func() {
		if !handedOff {
			s.runs.Done()
			_ = lease.Release()
		}
	}()
	if _, err := s.store.GetThread(ctx, tid); err != nil {
		if !errors.Is(err, model.ErrThreadNotFound) {
			return model.Run{}, err
		}
		if _, err = s.CreateThread(ctx, tid, nil, true); err != nil {
			return model.Run{}, err
		}
	}
	if s.inbox != nil {
		pending, err := s.inbox.HasPending(ctx, tid)
		if err != nil {
			return model.Run{}, err
		}
		if pending {
			return model.Run{}, ErrThreadBusy
		}
	}
	if input.AssistantID == "" {
		input.AssistantID = "lead_agent"
	}
	if input.OnDisconnect == "" {
		input.OnDisconnect = "cancel"
	}
	run := model.Run{RunID: model.NewID(), ThreadID: tid, AssistantID: input.AssistantID, Status: "pending", Created: time.Now().UTC(), Metadata: input.Metadata, OnDisconnect: input.OnDisconnect}
	if err := s.store.CreateRun(ctx, run); err != nil {
		return model.Run{}, err
	}
	runCtx, err := s.registry.Register(ctx, runtime.RunRecord{RunID: run.RunID, ThreadID: tid, AssistantID: run.AssistantID, OnDisconnect: runtime.DisconnectPolicy(run.OnDisconnect)})
	if err != nil {
		_, updateErr := s.store.UpdateRun(context.WithoutCancel(ctx), run.RunID, model.RunUpdate{Status: "failed"})
		return model.Run{}, errors.Join(err, updateErr)
	}
	runCtx, cancel := context.WithCancel(runCtx)
	stopShutdown := context.AfterFunc(s.shutdownCtx, cancel) //nolint:contextcheck // service shutdown is an independent cancellation authority
	stopLease := context.AfterFunc(lease.Context, cancel)    //nolint:contextcheck // database admission ownership independently cancels execution
	go func() {
		defer s.runs.Done()
		defer s.wakeInbox()
		defer func() {
			if err := lease.Release(); err != nil {
				s.logger.Error("releasing thread lease", "error", err)
			}
		}()
		defer cancel()
		defer stopShutdown()
		defer stopLease()
		s.manager.Execute(runCtx, runmanager.Run{RunID: run.RunID, ThreadID: tid, AssistantID: run.AssistantID, CreatedAt: run.Created, OnDisconnect: runtime.DisconnectPolicy(run.OnDisconnect)}, runmanager.Input{PullInputs: s.inputSource(tid, run.RunID, nil), Delegation: input.Delegation, Prompt: input.Prompt, ContentBlocks: input.ContentBlocks, Config: input.Config, Context: input.Context, PreparationError: input.PreparationError})
		if s.inbox != nil {
			bounded, stop := context.WithTimeout(context.WithoutCancel(runCtx), 5*time.Second)
			settled, getErr := s.store.GetRun(bounded, run.RunID)
			if getErr == nil {
				getErr = s.inbox.Finish(bounded, tid, run.RunID, settled.Status, s.shutdownCtx.Err() != nil)
			}
			stop()
			if getErr != nil {
				s.logger.Error("settling run inbox", "run_id", run.RunID, "error", getErr)
			}
		}
	}()
	handedOff = true
	return run, nil
}

func (s *Service) ownedRun(ctx context.Context, tid, rid string) (model.Run, error) {
	run, err := s.store.GetRun(ctx, rid)
	if err == nil && run.ThreadID != tid {
		return model.Run{}, model.ErrRunNotFound
	}
	return run, err
}
func (s *Service) GetRun(ctx context.Context, tid, rid string) (model.Run, bool, error) {
	run, err := s.ownedRun(ctx, tid, rid)
	if err != nil {
		return model.Run{}, false, err
	}
	return s.reconcileActiveRun(ctx, run)
}
func (s *Service) ListRuns(ctx context.Context, tid string) ([]model.Run, error) {
	runs, err := s.store.ListRuns(ctx, tid)
	if err != nil {
		return nil, err
	}
	for i := range runs {
		runs[i], _, err = s.reconcileActiveRun(ctx, runs[i])
		if err != nil {
			return nil, err
		}
	}
	return runs, nil
}
func (s *Service) CancelRun(ctx context.Context, tid, rid string) (bool, error) {
	run, err := s.ownedRun(ctx, tid, rid)
	if err != nil {
		return false, err
	}
	if run.Status != "pending" && run.Status != "running" {
		return false, nil
	}
	if err = s.registry.Cancel(ctx, rid); err != nil {
		if errors.Is(err, runtime.ErrRunNotFound) {
			return false, ErrRunNotRegistered
		}
		return false, err
	}
	// Terminal status belongs to RunManager after children and resources drain.
	return true, nil
}

type EventPage struct {
	Events    []runtime.Event `json:"events"`
	NextAfter int64           `json:"next_after"`
	HasMore   bool            `json:"has_more"`
}

func (s *Service) RawEvents(ctx context.Context, tid, rid string, after int64, limit int) (EventPage, error) {
	if _, err := s.ownedRun(ctx, tid, rid); err != nil {
		return EventPage{}, err
	}
	if after < 0 {
		after = 0
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 1000 {
		limit = 1000
	}
	events, err := s.events.Get(ctx, rid, after, limit+1)
	if err != nil {
		return EventPage{}, err
	}
	page := EventPage{Events: events, NextAfter: after, HasMore: len(events) > limit}
	if page.HasMore {
		page.Events = events[:limit]
	}
	if page.Events == nil {
		page.Events = []runtime.Event{}
	}
	if len(page.Events) > 0 {
		page.NextAfter = page.Events[len(page.Events)-1].Seq
	}
	return page, nil
}
