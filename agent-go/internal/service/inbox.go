package service

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/model"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/repository"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/inbox"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/runmanager"
)

func (s *Service) wakeInbox() {
	select {
	case s.inboxWake <- struct{}{}:
	default:
	}
}
func (s *Service) EnqueueInput(ctx context.Context, e inbox.Entry) (inbox.Entry, error) {
	if s.inbox == nil {
		return e, inbox.ErrUnavailable
	}
	if err := e.Validate(); err != nil {
		return e, err
	}
	s.runsMu.Lock()
	closing := s.closing
	s.runsMu.Unlock()
	if closing {
		return e, ErrClosing
	}
	if _, err := s.CreateThread(ctx, e.ThreadID, nil, true); err != nil {
		return e, err
	}
	e, err := s.inbox.Enqueue(ctx, e)
	if err == nil {
		s.wakeInbox()
	}
	return e, err
}
func (s *Service) ListInputs(ctx context.Context, tid string, after int64, limit int) ([]inbox.Entry, error) {
	if s.inbox == nil {
		return nil, inbox.ErrUnavailable
	}
	if _, err := s.store.GetThread(ctx, tid); err != nil {
		return nil, err
	}
	return s.inbox.ListInputs(ctx, tid, after, limit)
}
func (s *Service) GetInput(ctx context.Context, tid, id string) (inbox.Entry, error) {
	if s.inbox == nil {
		return inbox.Entry{}, inbox.ErrUnavailable
	}
	return s.inbox.GetInput(ctx, tid, id)
}
func (s *Service) CancelInput(ctx context.Context, tid, id string) (inbox.Entry, error) {
	if s.inbox == nil {
		return inbox.Entry{}, inbox.ErrUnavailable
	}
	return s.inbox.CancelInput(ctx, tid, id)
}

func (s *Service) ChangeInput(ctx context.Context, tid, id string, change inbox.Change) (inbox.Entry, error) {
	if s.inbox == nil {
		return inbox.Entry{}, inbox.ErrUnavailable
	}
	return s.inbox.ChangeInput(ctx, tid, id, change)
}

func (s *Service) inputSource(tid, rid string, initial *inbox.Entry) inbox.Source {
	if s.inbox == nil {
		return nil
	}
	initialPending := initial != nil
	return func(ctx context.Context) ([]message.Message, func(context.Context, []message.Message) error, error) {
		entries, err := s.inbox.Steering(ctx, tid, rid)
		if err != nil {
			return nil, nil, err
		}
		if initialPending {
			entries = append(entries, *initial)
			if initial.Delivery == inbox.NextStep {
				sort.Slice(entries, func(i, j int) bool { return entries[i].Position < entries[j].Position })
			}
		}
		if len(entries) == 0 {
			return nil, nil, nil
		}
		ids := make([]string, 0, len(entries))
		msgs := make([]message.Message, 0, len(entries))
		for _, e := range entries {
			ids = append(ids, e.ID)
			msgs = append(msgs, message.Message{Role: message.RoleUser, Content: e.Input.Prompt, ContentBlocks: e.Input.ContentBlocks, AdditionalKwargs: map[string]any{"inbox_message_id": e.ID}})
		}
		return msgs, func(commitCtx context.Context, history []message.Message) error {
			if err := s.inbox.Admit(commitCtx, tid, rid, ids, history); err != nil {
				return err
			}
			initialPending = false
			return nil
		}, nil
	}
}

// One scheduler per service polls the durable inbox; the thread lease chooses
// its execution owner across instances. It invokes the existing RunManager.
func (s *Service) scheduleInputs() {
	defer close(s.inboxDone)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	active := map[string]bool{}
	done := make(chan string, 8)
	scan := func() {
		tids, err := s.inbox.PendingThreads(s.shutdownCtx, 256)
		if err != nil {
			if s.shutdownCtx.Err() == nil {
				s.logger.Error("listing session inbox", "error", err)
			}
			return
		}
		for _, tid := range tids {
			if len(active) >= 8 {
				break
			}
			if active[tid] {
				continue
			}
			if !s.beginRun() {
				return
			}
			active[tid] = true
			go func(tid string) {
				defer s.runs.Done()
				defer func() {
					select {
					case done <- tid:
					case <-s.shutdownCtx.Done():
					}
				}()
				lease, err := s.store.AcquireThread(s.shutdownCtx, tid)
				if errors.Is(err, repository.ErrThreadBusy) {
					return
				}
				if err != nil {
					s.logger.Error("claiming session inbox lease", "thread_id", tid, "error", err)
					return
				}
				defer func() {
					if err := lease.Release(); err != nil {
						s.logger.Error("releasing inbox lease", "error", err)
					}
				}()
				ctx, cancel := context.WithCancel(s.shutdownCtx)
				defer cancel()
				stopLease := context.AfterFunc(lease.Context, cancel)
				defer stopLease() //nolint:contextcheck // independent database lease cancellation authority
				if err := s.drainInputs(ctx, tid); err != nil && ctx.Err() == nil {
					s.logger.Error("processing session inbox", "thread_id", tid, "error", err)
				}
			}(tid)
		}
	}
	scan()
	for {
		select {
		case <-s.shutdownCtx.Done():
			return
		case tid := <-done:
			delete(active, tid)
		case <-tick.C:
			scan()
		case <-s.inboxWake:
			scan()
		}
	}
}
func (s *Service) drainInputs(ctx context.Context, tid string) error {
	// Owning the exclusive thread lease is stronger proof than a stale registry
	// heartbeat. Settle orphaned runs before selecting a safe unadmitted claim.
	runs, err := s.store.ListRuns(ctx, tid)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if r.Status == "running" || r.Status == "pending" {
			if _, _, err = s.reconcileOwnedRun(ctx, r); err != nil {
				return err
			}
		}
	}
	for ctx.Err() == nil {
		e, found, err := s.inbox.Claim(ctx, tid, model.NewID())
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		run, err := s.store.GetRun(ctx, e.RunID)
		if err != nil {
			return err
		}
		runCtx, err := s.registry.Register(ctx, runtime.RunRecord{RunID: run.RunID, ThreadID: tid, AssistantID: run.AssistantID, OnDisconnect: runtime.DisconnectContinue})
		if err != nil {
			_, updateErr := s.store.UpdateRun(context.WithoutCancel(ctx), run.RunID, model.RunUpdate{Status: "failed"})
			finishErr := s.inbox.Finish(context.WithoutCancel(ctx), tid, run.RunID, "failed", ctx.Err() != nil)
			return errors.Join(err, updateErr, finishErr)
		}
		runCtx, cancel := context.WithCancel(runCtx)
		stopWorker := context.AfterFunc(ctx, cancel)
		current, err := s.inbox.GetInput(ctx, tid, e.ID)
		if err != nil || current.Status == "cancelled" {
			cancel()
		}
		s.manager.Execute(runCtx, runmanager.Run{RunID: run.RunID, ThreadID: tid, AssistantID: run.AssistantID, CreatedAt: run.Created, OnDisconnect: runtime.DisconnectContinue}, runmanager.Input{
			Config: e.Input.Config, Context: e.Input.Context, PullInputs: s.inputSource(tid, run.RunID, &e), PreparationError: err,
		})
		stopWorker()
		cancel()
		bounded, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		settled, getErr := s.store.GetRun(bounded, run.RunID)
		if getErr == nil {
			getErr = s.inbox.Finish(bounded, tid, run.RunID, settled.Status, ctx.Err() != nil)
		}
		stop()
		if getErr != nil {
			return getErr
		}
	}
	return ctx.Err()
}
