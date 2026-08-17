package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/metadata"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/replay"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/runmanager"
)

type Options struct {
	Agent             runmanager.Agent
	Store             Store
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
}

type Server struct {
	store        Store
	bus          runtime.Bus
	events       runtime.EventStore
	publisher    *runtime.EventPublisher
	manager      *runmanager.Manager
	heartbeat    time.Duration
	logger       *slog.Logger
	registry     *runtime.Registry
	ownRegistry  bool
	allowedTools []string
	pricer       *runtime.Pricer
	approvals    *runtime.ApprovalManager
	questions    *runtime.QuestionManager
	mux          *http.ServeMux

	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
	runsMu         sync.Mutex
	runs           sync.WaitGroup
	closing        bool
	closeOnce      sync.Once
}

func New(opts Options) (*Server, error) {
	if opts.Agent == nil {
		return nil, errors.New("httpapi: Agent is required")
	}
	if opts.Store == nil {
		opts.Store = NewMemoryStore()
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
		opts.MetadataStore = runtimeMetadataStore{store: opts.Store}
	}
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	s := &Server{store: opts.Store, bus: opts.Bus, events: opts.EventStore, publisher: publisher, heartbeat: opts.HeartbeatInterval, logger: opts.Logger, registry: opts.Registry, ownRegistry: opts.OwnRegistry, allowedTools: append([]string(nil), opts.AllowedTools...), pricer: opts.Pricer, approvals: opts.Approvals, questions: opts.Questions, shutdownCtx: shutdownCtx, shutdownCancel: shutdownCancel}
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
	s.mux = http.NewServeMux()
	s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Last-Event-ID")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.mux.ServeHTTP(w, r)
}

// Close stops run admission, cancels and drains active runs, then flushes the
// event replay store. It is safe to call more than once.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		s.runsMu.Lock()
		s.closing = true
		s.runsMu.Unlock()
		s.shutdownCancel()
		s.runs.Wait()
		s.publisher.Close()
		if s.ownRegistry {
			s.registry.Close()
		}
	})
}

func (s *Server) beginRun() bool {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	if s.closing {
		return false
	}
	s.runs.Add(1)
	return true
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /ok", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	s.mux.HandleFunc("POST /api/v1/threads", s.createThread)
	s.mux.HandleFunc("GET /api/v1/threads", s.searchThreads)
	s.mux.HandleFunc("GET /api/v1/threads/{tid}", s.getThread)
	s.mux.HandleFunc("PATCH /api/v1/threads/{tid}", s.patchThread)
	s.mux.HandleFunc("DELETE /api/v1/threads/{tid}", s.deleteThread)
	s.mux.HandleFunc("GET /api/v1/threads/{tid}/state", s.getState)
	s.mux.HandleFunc("POST /api/v1/threads/{tid}/state", s.postState)
	s.mux.HandleFunc("GET /api/v1/threads/{tid}/history", s.getHistory)
	s.mux.HandleFunc("POST /api/v1/threads/{tid}/runs", s.createRun)
	s.mux.HandleFunc("GET /api/v1/threads/{tid}/runs", s.listRuns)
	s.mux.HandleFunc("GET /api/v1/threads/{tid}/runs/{rid}", s.getRun)
	s.mux.HandleFunc("GET /api/v1/threads/{tid}/runs/{rid}/events", s.reconnectRun)
	s.mux.HandleFunc("POST /api/v1/threads/{tid}/runs/{rid}/cancel", s.cancelRun)
	s.mux.HandleFunc("GET /api/v1/threads/{tid}/runs/{rid}/questions/{qid}", s.getQuestion)
	s.mux.HandleFunc("POST /api/v1/threads/{tid}/runs/{rid}/questions/{qid}/answer", s.answerQuestion)
}

func (s *Server) getQuestion(w http.ResponseWriter, r *http.Request) {
	question, err := s.questions.Get(r.Context(), r.PathValue("qid"))
	if err != nil {
		if errors.Is(err, runtime.ErrQuestionNotFound) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	if question.ThreadID != r.PathValue("tid") || question.RunID != r.PathValue("rid") {
		writeError(w, http.StatusNotFound, runtime.ErrQuestionNotFound)
		return
	}
	writeJSON(w, http.StatusOK, question)
}

func (s *Server) answerQuestion(w http.ResponseWriter, r *http.Request) {
	question, err := s.questions.Get(r.Context(), r.PathValue("qid"))
	if err != nil {
		if errors.Is(err, runtime.ErrQuestionNotFound) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	if question.ThreadID != r.PathValue("tid") || question.RunID != r.PathValue("rid") {
		writeError(w, http.StatusNotFound, runtime.ErrQuestionNotFound)
		return
	}
	var body struct {
		Selected []string `json:"selected"`
		Custom   string   `json:"custom"`
		Dismiss  bool     `json:"dismiss"`
		By       string   `json:"by"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.By) == "" {
		body.By = "user"
	}
	if body.Dismiss {
		question, err = s.questions.Dismiss(r.Context(), question.ID, body.By)
	} else {
		question, err = s.questions.Answer(r.Context(), question.ID, runtime.QuestionAnswer{Selected: body.Selected, Custom: body.Custom}, body.By)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, question)
}

func newThread(id string, metadata map[string]any) Thread {
	now := time.Now().UTC()
	return Thread{ThreadID: id, Created: now, Updated: now, Metadata: metadata, Status: "idle", Values: map[string]any{}}
}

func (s *Server) createThread(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ThreadID string         `json:"thread_id"`
		Metadata map[string]any `json:"metadata"`
		IfExists string         `json:"if_exists"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ThreadID == "" {
		body.ThreadID = newID()
	}
	t, err := s.store.CreateThread(r.Context(), newThread(body.ThreadID, body.Metadata), body.IfExists == "do_nothing")
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, t)
}
func (s *Server) searchThreads(w http.ResponseWriter, r *http.Request) {
	ts, err := s.store.SearchThreads(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, ts)
}
func (s *Server) getThread(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.GetThread(r.Context(), r.PathValue("tid"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, t)
}
func (s *Server) patchThread(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Metadata map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	t, err := s.store.UpdateThread(r.Context(), r.PathValue("tid"), body.Metadata, nil)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, t)
}
func (s *Server) deleteThread(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteThread(r.Context(), r.PathValue("tid")); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) state(ctx context.Context, tid string) (map[string]any, error) {
	t, err := s.store.GetThread(ctx, tid)
	if err != nil {
		return nil, err
	}
	hist, err := s.store.LoadHistory(ctx, tid)
	if err != nil {
		return nil, err
	}
	vals := cloneMap(t.Values)
	if vals == nil {
		vals = map[string]any{}
	}
	vals["messages"] = wireMessages(hist)
	state := map[string]any{"values": vals, "metadata": t.Metadata, "created_at": t.Updated}
	completion, ok, err := s.store.LatestRunCompletion(ctx, tid)
	if err != nil {
		return nil, err
	}
	if ok {
		state["token_usage"] = tokenUsagePayload(completion.InputTokens, completion.OutputTokens, completion.CachedInputTokens, completion.LeadTokens, completion.SubagentTokens, completion.AuxiliaryTokens)
	}
	return state, nil
}
func (s *Server) getState(w http.ResponseWriter, r *http.Request) {
	v, err := s.state(r.Context(), r.PathValue("tid"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) postState(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Values map[string]any `json:"values"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	_, err := s.store.UpdateThread(r.Context(), r.PathValue("tid"), nil, body.Values)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.getState(w, r)
}
func (s *Server) getHistory(w http.ResponseWriter, r *http.Request) {
	v, err := s.state(r.Context(), r.PathValue("tid"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, []any{v})
}

func (s *Server) createRun(w http.ResponseWriter, r *http.Request) {
	var body runCreate
	if !decodeJSON(w, r, &body) {
		return
	}
	if _, removed := body.Config["configurable"]; removed {
		writeError(w, http.StatusBadRequest, errors.New("config.configurable is not supported; use flat config or context fields"))
		return
	}
	if !s.beginRun() {
		writeError(w, http.StatusServiceUnavailable, errors.New("server is shutting down"))
		return
	}
	handedOff := false
	defer func() {
		if !handedOff {
			s.runs.Done()
		}
	}()
	tid := r.PathValue("tid")
	if _, err := s.store.GetThread(r.Context(), tid); errors.Is(err, ErrThreadNotFound) {
		_, _ = s.store.CreateThread(r.Context(), newThread(tid, nil), false)
	}
	if body.AssistantID == "" {
		body.AssistantID = "lead_agent"
	}
	if body.OnDisconnect == "" {
		body.OnDisconnect = "cancel"
	}
	rid := newID()
	run := Run{RunID: rid, ThreadID: tid, AssistantID: body.AssistantID, Status: "pending", Created: time.Now().UTC(), Metadata: body.Metadata, OnDisconnect: body.OnDisconnect}
	if err := s.store.CreateRun(r.Context(), run); err != nil {
		writeError(w, 500, err)
		return
	}
	runCtx, err := s.registry.Register(r.Context(), runtime.RunRecord{RunID: rid, ThreadID: tid, AssistantID: body.AssistantID, OnDisconnect: runtime.DisconnectPolicy(body.OnDisconnect)})
	if err != nil {
		writeError(w, 500, err)
		return
	}
	runCtx, cancelRun := context.WithCancel(runCtx)
	stopShutdownCancel := context.AfterFunc(s.shutdownCtx, cancelRun)
	prompt, contentBlocks, inputErr := inputFromMessages(body.Input)
	runtimeRun := runmanager.Run{
		RunID: run.RunID, ThreadID: run.ThreadID, AssistantID: run.AssistantID,
		CreatedAt: run.Created, OnDisconnect: runtime.DisconnectPolicy(run.OnDisconnect),
	}
	runtimeInput := runmanager.Input{
		Prompt: prompt, ContentBlocks: contentBlocks, Config: body.Config, Context: body.Context,
		PreparationError: inputErr,
	}
	go func() {
		defer s.runs.Done()
		defer cancelRun()
		defer stopShutdownCancel()
		s.manager.Execute(runCtx, runtimeRun, runtimeInput)
	}()
	handedOff = true
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Content-Location", fmt.Sprintf("/api/v1/threads/%s/runs/%s/events", tid, rid))
	s.stream(w, r, rid, 0)
}

func tokenUsagePayload(input, output, cached, lead, subagent, auxiliary int) map[string]any {
	return map[string]any{
		"type":              "token_usage",
		"input_tokens":      input,
		"output_tokens":     output,
		"cache_read_tokens": cached,
		"total_tokens":      input + output,
		"lead_tokens":       lead,
		"subagent_tokens":   subagent,
		"auxiliary_tokens":  auxiliary,
	}
}

func (s *Server) publishProjection(ctx context.Context, run runmanager.Run, projection runmanager.Projection) error {
	name := ""
	data := projection.Data
	switch projection.Kind {
	case runmanager.ProjectionRunStart:
		name = "metadata"
	case runmanager.ProjectionValues:
		name = "values"
	case runmanager.ProjectionMessage:
		msg, ok := projection.Data.(message.Message)
		if !ok {
			return fmt.Errorf("httpapi: message projection has type %T", projection.Data)
		}
		name = "messages"
		data = []any{toWireMessage(msg), map[string]any{"run_id": run.RunID, "thread_id": run.ThreadID}}
	case runmanager.ProjectionUsage:
		totals, ok := projection.Data.(runtime.Totals)
		if !ok {
			return fmt.Errorf("httpapi: usage projection has type %T", projection.Data)
		}
		name = "custom"
		data = tokenUsagePayload(totals.InputTokens, totals.OutputTokens, totals.CachedInputTokens, totals.LeadTokens, totals.SubagentTokens, totals.AuxiliaryTokens)
	case runmanager.ProjectionError:
		cause, ok := projection.Data.(error)
		if !ok {
			return fmt.Errorf("httpapi: error projection has type %T", projection.Data)
		}
		name = "error"
		data = map[string]any{"message": cause.Error(), "type": fmt.Sprintf("%T", cause)}
	case runmanager.ProjectionRunEnd:
		name = "end"
	default:
		return fmt.Errorf("httpapi: unknown runtime projection %q", projection.Kind)
	}
	typ := wireEventType(name, data)
	e := runtime.MustEvent(run.RunID, run.ThreadID, typ, makeWireEvent(name, data))
	_, err := s.publishEvent(ctx, e)
	return err
}

func wireEventType(name string, data any) runtime.EventType {
	var typ runtime.EventType
	switch name {
	case "metadata":
		typ = runtime.EventRunStart
	case "values":
		typ = runtime.EventStateValues
	case "messages":
		typ = runtime.EventMessage
	case "custom":
		typ = runtime.EventCustom
		if payload, ok := data.(map[string]any); ok && payload["type"] == "token_usage" {
			typ = runtime.EventUsage
		}
	case "error":
		typ = runtime.EventError
	case "end":
		typ = runtime.EventRunEnd
	default:
		typ = runtime.EventCustom
	}
	return typ
}

func (s *Server) publishEvent(ctx context.Context, e runtime.Event) (int64, error) {
	return s.publisher.Publish(ctx, e)
}
func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.store.ListRuns(r.Context(), r.PathValue("tid"))
	if err != nil {
		writeError(w, 500, err)
		return
	}
	for i := range runs {
		runs[i], _, err = s.reconcileActiveRun(r.Context(), runs[i])
		if err != nil {
			writeError(w, 500, err)
			return
		}
	}
	writeJSON(w, 200, runs)
}
func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.store.GetRun(r.Context(), r.PathValue("rid"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	run, _, err = s.reconcileActiveRun(r.Context(), run)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, run)
}
func (s *Server) reconnectRun(w http.ResponseWriter, r *http.Request) {
	rid := r.PathValue("rid")
	run, err := s.store.GetRun(r.Context(), rid)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	_, orphaned, err := s.reconcileActiveRun(r.Context(), run)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	after, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	if orphaned {
		_ = writeSSE(w, 0, makeWireEvent("end", map[string]any{"status": "interrupted", "error": "run interrupted because its worker stopped"}))
		return
	}
	s.stream(w, r, rid, after)
}

func (s *Server) reconcileActiveRun(ctx context.Context, run Run) (Run, bool, error) {
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
	state, err := s.restoreRunState(ctx, run)
	if err != nil {
		return run, false, err
	}
	if snapshot := state.Snapshot(); snapshot.Phase.Terminal() {
		updated, updateErr := s.store.UpdateRun(ctx, run.RunID, RunUpdate{Status: wireStatusForPhase(snapshot.Phase)})
		return updated, false, updateErr
	}
	updated, err := s.store.UpdateRun(ctx, run.RunID, RunUpdate{Status: "interrupted", RiskLevel: "unknown"})
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
	completion := RunCompletion{
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

func (s *Server) restoreRunState(ctx context.Context, run Run) (*runtime.RunStateMachine, error) {
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
func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	rid := r.PathValue("rid")
	run, err := s.store.GetRun(r.Context(), rid)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if run.Status != "pending" && run.Status != "running" {
		writeJSON(w, 200, map[string]any{"ok": true, "was_running": false})
		return
	}
	if err := s.registry.Cancel(r.Context(), rid); err != nil {
		if errors.Is(err, runtime.ErrRunNotFound) {
			writeError(w, http.StatusConflict, errors.New("run is no longer registered and cannot be cancelled"))
			return
		}
		writeError(w, 500, err)
		return
	}
	if _, err := s.store.UpdateRun(r.Context(), rid, RunUpdate{Status: "cancelled"}); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "was_running": true})
}

// stream subscribes before replay, drains persistent pages, overlays the
// in-memory backlog, and deduplicates by sequence. This ordering closes the
// race between replay and subscribing.
func (s *Server) stream(w http.ResponseWriter, r *http.Request, rid string, after int64) {
	live, unsubscribe := s.bus.Subscribe(rid)
	disconnectCtx := context.WithoutCancel(r.Context())
	defer func() {
		unsubscribe()
		_ = s.registry.ClientDisconnected(disconnectCtx, rid, s.bus.SubscriberCount(rid))
	}()
	seen := after
	ended := false
	yield := func(e runtime.Event) bool {
		if e.Seq <= seen {
			return true
		}
		// Transcript events are the canonical model-facing persistence stream.
		// They must advance the SSE cursor, but are intentionally not exposed as
		// frontend wire events; the HTTP messages/values projections remain the
		// transport contract.
		if !clientVisibleRuntimeEvent(e) {
			seen = e.Seq
			return true
		}
		we, err := decodeWireEvent(e)
		if err != nil {
			s.logger.Warn("invalid wire event", "error", err)
			return true
		}
		if err := writeSSE(w, e.Seq, we); err != nil {
			return false
		}
		seen = e.Seq
		if we.Event == "end" {
			ended = true
			return false
		}
		return true
	}
	_ = runtime.Replay(r.Context(), s.events, rid, after, 200, yield)
	if !ended {
		for _, e := range s.bus.Backlog(rid, seen) {
			if !yield(e) {
				break
			}
		}
	}
	if ended {
		return
	}
	ticker := time.NewTicker(s.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-live:
			if !ok {
				return
			}
			if !yield(e) {
				return
			}
		case <-ticker.C:
			_ = runtime.Replay(r.Context(), s.events, rid, seen, 200, yield)
			if ended {
				return
			}
			_, _ = fmt.Fprint(w, ": heartbeat\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
}

func inputFromMessages(input map[string]any) (string, []message.ContentBlock, error) {
	raw, ok := input["messages"].([]any)
	if !ok {
		return "", nil, nil
	}
	for i := len(raw) - 1; i >= 0; i-- {
		m, ok := raw[i].(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		role, _ := m["role"].(string)
		if typ == "human" || typ == "user" || role == "user" {
			return parseUserContent(m["content"])
		}
	}
	return "", nil, nil
}

func parseUserContent(content any) (string, []message.ContentBlock, error) {
	if text, ok := content.(string); ok {
		return text, nil, nil
	}

	parts, ok := content.([]any)
	if !ok {
		return "", nil, fmt.Errorf("user message content must be a string or an array")
	}
	texts := make([]string, 0, len(parts))
	blocks := make([]message.ContentBlock, 0, len(parts))
	for index, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			return "", nil, fmt.Errorf("content block %d must be an object", index)
		}
		partType, _ := part["type"].(string)
		switch partType {
		case "text":
			text, ok := part["text"].(string)
			if !ok {
				return "", nil, fmt.Errorf("text block %d requires text", index)
			}
			texts = append(texts, text)
		case "image_url":
			imageURL, err := imageURLFromPart(part["image_url"])
			if err != nil {
				return "", nil, fmt.Errorf("image block %d: %w", index, err)
			}
			block, err := imageBlockFromDataURL(imageURL)
			if err != nil {
				return "", nil, fmt.Errorf("image block %d: %w", index, err)
			}
			blocks = append(blocks, block)
		default:
			return "", nil, fmt.Errorf("content block %d has unsupported type %q", index, partType)
		}
	}
	return strings.Join(texts, "\n"), blocks, nil
}

func imageURLFromPart(value any) (string, error) {
	switch image := value.(type) {
	case string:
		return image, nil
	case map[string]any:
		url, ok := image["url"].(string)
		if !ok || url == "" {
			return "", errors.New("image_url requires url")
		}
		return url, nil
	default:
		return "", errors.New("image_url must be a string or an object")
	}
}

func imageBlockFromDataURL(value string) (message.ContentBlock, error) {
	metadata, encoded, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	if !strings.HasPrefix(value, "data:") || !ok {
		return message.ContentBlock{}, errors.New("only data URLs are supported")
	}
	segments := strings.Split(metadata, ";")
	mimeType := segments[0]
	if !strings.HasPrefix(mimeType, "image/") {
		return message.ContentBlock{}, fmt.Errorf("unsupported media type %q", mimeType)
	}
	if len(segments) < 2 || segments[len(segments)-1] != "base64" {
		return message.ContentBlock{}, errors.New("image data URL must be base64 encoded")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return message.ContentBlock{}, fmt.Errorf("invalid base64 data: %w", err)
	}
	return message.ContentBlock{Type: "image", MimeType: mimeType, Data: base64.StdEncoding.EncodeToString(decoded)}, nil
}
func wireMessages(msgs []message.Message) []wireMessage {
	out := make([]wireMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, toWireMessage(m))
	}
	return out
}
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b[:])
	return strings.Join([]string{s[:8], s[8:12], s[12:16], s[16:20], s[20:]}, "-")
}
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	defer func() { _ = r.Body.Close() }()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		writeError(w, 400, err)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("request body must contain one JSON value")
		}
		writeError(w, 400, err)
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"detail": err.Error()})
}
func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrThreadNotFound) || errors.Is(err, ErrRunNotFound) {
		writeError(w, 404, err)
		return
	}
	writeError(w, 500, err)
}
