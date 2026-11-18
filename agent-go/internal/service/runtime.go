package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/model"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/repository"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/delegation"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/inbox"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/metadata"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/plugincontrol"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/replay"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/runmanager"
)

type Options struct {
	Plugins           plugincontrol.Controller
	Subagents         delegation.SessionStore
	Agent             runmanager.Agent
	Store             repository.Store
	Bus               runtime.Bus
	EventStore        runtime.EventStore
	HeartbeatInterval time.Duration
	Logger            *slog.Logger
	Registry          *runtime.Registry
	OwnRegistry       bool
	AllowedTools      []string
	Pricer            *runtime.Pricer
	Approvals         *runtime.ApprovalManager
	Questions         *runtime.QuestionManager
	Snapshots         replay.SnapshotStore
	MetadataStore     metadata.Store
	ProjectEvent      func(runmanager.Run, runmanager.Projection) (runtime.Event, error)
}

type Service struct {
	pluginsCtl  plugincontrol.Controller
	inbox       inbox.Store
	inboxWake   chan struct{}
	inboxDone   chan struct{}
	subagents   delegation.SessionStore
	store       repository.Store
	bus         runtime.Bus
	events      runtime.EventStore
	snapshots   replay.SnapshotStore
	publisher   *runtime.EventPublisher
	manager     *runmanager.Manager
	heartbeat   time.Duration
	logger      *slog.Logger
	registry    *runtime.Registry
	ownRegistry bool
	questions   *runtime.QuestionManager

	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
	runsMu         sync.Mutex
	runs           sync.WaitGroup
	closing        bool
	closeOnce      sync.Once
	projectEvent   func(runmanager.Run, runmanager.Projection) (runtime.Event, error)
}

func New(opts Options) (*Service, error) {
	if opts.ProjectEvent == nil {
		return nil, errors.New("service: projection adapter is required")
	}
	if opts.Agent == nil {
		return nil, errors.New("httpapi: Agent is required")
	}
	if opts.Store == nil {
		opts.Store = repository.NewMemoryStore()
	}
	if opts.EventStore == nil {
		opts.EventStore = runtime.NewMemoryEventStore()
	}
	if opts.Bus == nil {
		opts.Bus = runtime.NewMemoryBus(runtime.BusOptions{})
	}
	publisher, err := runtime.NewEventPublisher(opts.Bus, opts.EventStore, runtime.PersisterOptions{})
	if err != nil {
		return nil, err
	}
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = 15 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Registry == nil {
		var registryErr error
		opts.Registry, registryErr = runtime.NewRegistry(context.Background(), runtime.RegistryOptions{})
		if registryErr != nil {
			publisher.Close()
			return nil, registryErr
		}
		opts.OwnRegistry = true
	}
	if opts.Approvals == nil {
		var approvalErr error
		opts.Approvals, approvalErr = runtime.NewApprovalManager(runtime.NewMemoryApprovalStore())
		if approvalErr != nil {
			publisher.Close()
			return nil, approvalErr
		}
	}
	if opts.Questions == nil {
		var questionErr error
		opts.Questions, questionErr = runtime.NewQuestionManager(runtime.NewMemoryQuestionStore())
		if questionErr != nil {
			publisher.Close()
			return nil, questionErr
		}
	}
	if opts.Snapshots == nil {
		opts.Snapshots = replay.NewMemorySnapshotStore()
	}
	if opts.MetadataStore == nil {
		opts.MetadataStore = repository.NewMetadataStore(opts.Store)
	}
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	s := &Service{pluginsCtl: opts.Plugins, subagents: opts.Subagents, store: opts.Store, bus: opts.Bus, events: opts.EventStore, snapshots: opts.Snapshots, publisher: publisher, heartbeat: opts.HeartbeatInterval, logger: opts.Logger, registry: opts.Registry, ownRegistry: opts.OwnRegistry, questions: opts.Questions, shutdownCtx: shutdownCtx, shutdownCancel: shutdownCancel, projectEvent: opts.ProjectEvent}
	s.manager, err = runmanager.New(runmanager.Options{
		Agent: opts.Agent, Store: opts.MetadataStore, Registry: opts.Registry,
		PublishEvent: s.publishEvent, Project: s.publishProjection, Approvals: opts.Approvals, Questions: opts.Questions,
		AllowedTools: opts.AllowedTools, Pricer: opts.Pricer, Heartbeat: opts.HeartbeatInterval,
		Logger: opts.Logger, Snapshots: opts.Snapshots,
	})
	if err != nil {
		publisher.Close()
		shutdownCancel()
		return nil, err
	}
	if store, ok := opts.Store.(inbox.Store); ok {
		s.inbox = store
		s.inboxWake = make(chan struct{}, 1)
		s.inboxDone = make(chan struct{})
		go s.scheduleInputs()
	}
	return s, nil
}

func (s *Service) Close() {
	s.closeOnce.Do(func() {
		s.runsMu.Lock()
		s.closing = true
		s.runsMu.Unlock()
		s.shutdownCancel()
		if s.inboxDone != nil {
			<-s.inboxDone
		}
		s.runs.Wait()
		s.publisher.Close()
		if s.ownRegistry {
			s.registry.Close()
		}
	})
}

func (s *Service) beginRun() bool {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	if s.closing {
		return false
	}
	s.runs.Add(1)
	return true
}
func (s *Service) publishProjection(ctx context.Context, run runmanager.Run, projection runmanager.Projection) error {
	event, err := s.projectEvent(run, projection)
	if err != nil {
		return err
	}
	_, err = s.publishEvent(ctx, event)
	return err
}
func (s *Service) publishEvent(ctx context.Context, e runtime.Event) (int64, error) {
	return s.publisher.Publish(ctx, e)
}

func (s *Service) reconcileActiveRun(ctx context.Context, run model.Run) (model.Run, bool, error) {
	if run.Status != "pending" && run.Status != "running" {
		return run, false, nil
	}
	record, err := s.registry.Get(ctx, run.RunID)
	orphaned := errors.Is(err, runtime.ErrRunNotFound)
	if err != nil && !orphaned {
		// A temporary registry outage must not terminate a healthy model call.
		s.logger.Warn("checking run liveness", "run_id", run.RunID, "error", err)
		return run, false, nil
	}
	if err == nil {
		lastSeen := record.HeartbeatAt
		if lastSeen.IsZero() {
			lastSeen = record.StartedAt
		}
		grace := 3 * s.heartbeat
		if grace < time.Minute {
			grace = time.Minute
		}
		orphaned = record.Status.Terminal() || (!lastSeen.IsZero() && time.Since(lastSeen) > grace)
	}
	if !orphaned {
		return run, false, nil
	}
	lease, leaseErr := s.store.AcquireThread(ctx, run.ThreadID)
	if errors.Is(leaseErr, repository.ErrThreadBusy) {
		return run, false, nil
	}
	if leaseErr != nil {
		return run, false, leaseErr
	}
	defer func() {
		if err := lease.Release(); err != nil {
			s.logger.Error("releasing reconciliation lease", "error", err)
		}
	}()
	return s.reconcileOwnedRun(ctx, run)
}

// reconcileOwnedRun requires the caller to retain the exclusive thread lease.
func (s *Service) reconcileOwnedRun(ctx context.Context, run model.Run) (model.Run, bool, error) {
	state, err := s.restoreRunState(ctx, run)
	if err != nil {
		return run, false, err
	}
	if snapshot := state.Snapshot(); snapshot.Phase.Terminal() {
		updated, updateErr := s.store.UpdateRun(ctx, run.RunID, model.RunUpdate{Status: wireStatusForPhase(snapshot.Phase)})
		return updated, false, updateErr
	}
	updated, err := s.store.UpdateRun(ctx, run.RunID, model.RunUpdate{Status: "interrupted", RiskLevel: "unknown"})
	if err != nil {
		return run, false, err
	}
	if updated.Status != "interrupted" {
		return updated, false, nil
	}
	state.SetObserver(func(snapshot runtime.RunSnapshot) error {
		event := runtime.MustEvent(run.RunID, run.ThreadID, runtime.EventRunStateChanged, runtime.RunStateChanged{Snapshot: snapshot})
		event.IdempotencyKey = fmt.Sprintf("run:%s:state:%d", run.RunID, snapshot.Version)
		_, err := s.publishEvent(context.WithoutCancel(ctx), event)
		return err
	})
	if err := state.Interrupt("worker lease expired after process interruption"); err != nil {
		return run, false, err
	}
	completion := model.RunCompletion{
		RunID: run.RunID, ThreadID: run.ThreadID, Status: "interrupted",
		Duration: time.Since(run.Created), CompletedAt: time.Now().UTC(),
	}
	if err := s.store.SaveRunCompletion(context.WithoutCancel(ctx), completion); err != nil {
		return run, false, fmt.Errorf("saving interrupted run completion: %w", err)
	}
	_ = s.registry.Complete(ctx, run.RunID, runtime.StatusInterrupted)
	end := runtime.MustEvent(run.RunID, run.ThreadID, runtime.EventRunEnd, runtime.RunEnd{Status: "interrupted", Error: "run interrupted because its worker stopped"})
	end.IdempotencyKey = "run:" + run.RunID + ":end"
	if _, err := s.publishEvent(context.WithoutCancel(ctx), end); err != nil {
		return run, false, err
	}
	s.logger.Warn("reconciled orphaned run", "run_id", run.RunID, "thread_id", run.ThreadID)
	return updated, true, nil
}

func (s *Service) restoreRunState(ctx context.Context, run model.Run) (*runtime.RunStateMachine, error) {
	projector := replay.NewStateProjector()
	var projectionErr error
	if err := runtime.Replay(ctx, s.events, run.RunID, 0, 200, func(event runtime.Event) bool {
		if applyErr := projector.Apply(event); applyErr != nil {
			projectionErr = applyErr
			return false
		}
		return true
	}); err != nil {
		return nil, fmt.Errorf("replaying run state for recovery: %w", err)
	}
	if projectionErr != nil {
		return nil, fmt.Errorf("projecting run state for recovery: %w", projectionErr)
	}
	snapshot := projector.Snapshot()
	if snapshot.RunID == "" {
		return runtime.NewRunStateMachine(run.RunID, run.ThreadID)
	}
	return runtime.RestoreRunStateMachine(snapshot)
}

func wireStatusForPhase(phase runtime.RunPhase) string {
	switch phase {
	case runtime.RunCompleted:
		return "success"
	case runtime.RunCancelled:
		return "cancelled"
	case runtime.RunInterrupted:
		return "interrupted"
	default:
		return "error"
	}
}
