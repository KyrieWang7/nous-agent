package langgraphapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

type Options struct {
	Agent             Agent
	Store             Store
	Bus               runtime.Bus
	EventStore        runtime.EventStore
	HeartbeatInterval time.Duration
	Logger            *slog.Logger
	Registry          *runtime.Registry
	OwnRegistry       bool
	AllowedTools      []string
}

type Server struct {
	agent        Agent
	store        Store
	bus          runtime.Bus
	events       runtime.EventStore
	persister    *runtime.AsyncPersister
	heartbeat    time.Duration
	logger       *slog.Logger
	registry     *runtime.Registry
	ownRegistry  bool
	allowedTools []string
	mux          *http.ServeMux
}

func New(opts Options) (*Server, error) {
	if opts.Agent == nil {
		return nil, errors.New("langgraphapi: Agent is required")
	}
	if opts.Store == nil {
		opts.Store = NewMemoryStore()
	}
	if opts.EventStore == nil {
		opts.EventStore = runtime.NewMemoryEventStore()
	}
	persister := runtime.NewAsyncPersister(opts.EventStore, runtime.PersisterOptions{})
	if opts.Bus == nil {
		opts.Bus = runtime.NewMemoryBus(runtime.BusOptions{})
	}
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = 15 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Registry == nil {
		var err error
		opts.Registry, err = runtime.NewRegistry(context.Background(), runtime.RegistryOptions{})
		if err != nil {
			return nil, err
		}
		opts.OwnRegistry = true
	}
	s := &Server{agent: opts.Agent, store: opts.Store, bus: opts.Bus, events: opts.EventStore, persister: persister, heartbeat: opts.HeartbeatInterval, logger: opts.Logger, registry: opts.Registry, ownRegistry: opts.OwnRegistry, allowedTools: append([]string(nil), opts.AllowedTools...)}
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

// Close flushes the event replay store. It is safe to call more than once.
func (s *Server) Close() {
	s.persister.Close()
	if s.ownRegistry {
		s.registry.Close()
	}
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /ok", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	s.mux.HandleFunc("GET /info", s.info)
	s.mux.HandleFunc("POST /assistants/search", s.assistants)
	s.mux.HandleFunc("GET /assistants/{id}/graph", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"nodes": []any{}, "edges": []any{}})
	})
	s.mux.HandleFunc("GET /assistants/{id}/schemas", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"graph_id": r.PathValue("id"), "state_schema": map[string]any{}, "config_schema": map[string]any{}})
	})
	s.mux.HandleFunc("POST /threads", s.createThread)
	s.mux.HandleFunc("POST /threads/search", s.searchThreads)
	s.mux.HandleFunc("GET /threads/{tid}", s.getThread)
	s.mux.HandleFunc("PATCH /threads/{tid}", s.patchThread)
	s.mux.HandleFunc("DELETE /threads/{tid}", s.deleteThread)
	s.mux.HandleFunc("GET /threads/{tid}/state", s.getState)
	s.mux.HandleFunc("POST /threads/{tid}/state", s.postState)
	s.mux.HandleFunc("GET /threads/{tid}/history", s.getHistory)
	s.mux.HandleFunc("POST /threads/{tid}/history", s.getHistory)
	s.mux.HandleFunc("POST /threads/{tid}/runs/stream", s.createRun)
	s.mux.HandleFunc("POST /runs/stream", s.createStatelessRun)
	s.mux.HandleFunc("GET /threads/{tid}/runs", s.listRuns)
	s.mux.HandleFunc("GET /threads/{tid}/runs/{rid}", s.getRun)
	s.mux.HandleFunc("GET /threads/{tid}/runs/{rid}/stream", s.reconnectRun)
	s.mux.HandleFunc("POST /threads/{tid}/runs/{rid}/cancel", s.cancelRun)
	s.mux.HandleFunc("GET /threads/{tid}/stream", s.threadStream)

	// Native Go API. It intentionally shares the same event projection as the
	// compatibility routes (metadata/values/messages/custom/error/end), while
	// avoiding any graph/checkpoint vocabulary in its resource model.
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
}

func (s *Server) info(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"flags": map[string]any{}, "event_size_limit": 10_000_000, "version": "agent-go"})
}
func (s *Server) assistants(w http.ResponseWriter, _ *http.Request) {
	now := time.Now().UTC()
	writeJSON(w, 200, []any{map[string]any{"assistant_id": "lead_agent", "graph_id": "lead_agent", "config": map[string]any{}, "metadata": map[string]any{}, "created_at": now, "updated_at": now, "name": "lead_agent", "version": 1}})
}

func newThread(id string, metadata map[string]any) Thread {
	now := time.Now().UTC()
	return Thread{ThreadID: id, Created: now, Updated: now, Metadata: metadata, Status: "idle", Values: map[string]any{}, Config: map[string]any{"configurable": map[string]any{"thread_id": id}}}
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
	state := map[string]any{"values": vals, "next": []any{}, "checkpoint": map[string]string{"thread_id": tid, "checkpoint_ns": "", "checkpoint_id": ""}, "metadata": t.Metadata, "created_at": t.Updated, "parent_checkpoint": nil, "tasks": []any{}}
	completion, ok, err := s.store.LatestRunCompletion(ctx, tid)
	if err != nil {
		return nil, err
	}
	if ok {
		state["token_usage"] = tokenUsagePayload(completion.InputTokens, completion.OutputTokens, completion.CachedInputTokens, completion.LeadTokens, completion.SubagentTokens, completion.MiddlewareTokens)
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

func (s *Server) createStatelessRun(w http.ResponseWriter, r *http.Request) {
	tid := newID()
	_, _ = s.store.CreateThread(r.Context(), newThread(tid, nil), false)
	r.SetPathValue("tid", tid)
	s.createRun(w, r)
}
func (s *Server) createRun(w http.ResponseWriter, r *http.Request) {
	var body runCreate
	if !decodeJSON(w, r, &body) {
		return
	}
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
	go s.execute(runCtx, run, body)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Content-Location", fmt.Sprintf("/threads/%s/runs/%s/stream", tid, rid))
	s.stream(w, r, rid, 0)
}

func (s *Server) execute(ctx context.Context, run Run, body runCreate) {
	startedAt := time.Now()
	keepAliveCtx, stopKeepAlive := context.WithCancel(ctx)
	defer stopKeepAlive()
	go s.registry.KeepAlive(keepAliveCtx, run.RunID, s.heartbeat)
	persistCtx := context.WithoutCancel(ctx)
	s.publish(persistCtx, run, "metadata", map[string]string{"run_id": run.RunID})
	journal := runtime.NewJournal(nil)
	result := AgentResult{}
	var runErr error

	if _, err := s.store.UpdateRun(persistCtx, run.RunID, RunUpdate{Status: "running"}); err != nil {
		runErr = fmt.Errorf("starting run: %w", err)
	}

	var hist []message.Message
	var thread Thread
	var prompt string
	var contentBlocks []message.ContentBlock
	if runErr == nil {
		var err error
		hist, err = s.store.LoadHistory(ctx, run.ThreadID)
		if err != nil {
			runErr = fmt.Errorf("loading history: %w", err)
		}
	}
	if runErr == nil {
		var err error
		thread, err = s.store.GetThread(ctx, run.ThreadID)
		if err != nil {
			runErr = fmt.Errorf("loading thread: %w", err)
		}
	}
	if runErr == nil {
		var err error
		prompt, contentBlocks, err = inputFromMessages(body.Input)
		if err != nil {
			runErr = fmt.Errorf("parsing run input: %w", err)
		}
	}
	if runErr == nil {
		ctx = runtime.WithRunContext(ctx, runtime.RunContext{RunID: run.RunID, ThreadID: run.ThreadID, AllowedTools: s.allowedTools, Journal: journal, Bus: s.bus, Publish: s.publishEvent})
		result, runErr = s.agent.Run(ctx, AgentRequest{RunID: run.RunID, ThreadID: run.ThreadID, AssistantID: run.AssistantID, Prompt: prompt, ContentBlocks: contentBlocks, History: hist, Config: body.Config, Context: mergeMaps(thread.Values, body.Context)})
	}

	status := "success"
	if runErr != nil {
		status = "error"
		if errors.Is(runErr, context.Canceled) {
			status = "cancelled"
		}
	} else {
		if journal.Totals().LLMCalls == 0 {
			journal.Observe(runtime.Entry{Bucket: runtime.BucketLead, Source: "lead", CallID: run.RunID, Usage: model.Usage{InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens}})
		}
		if len(result.Messages) == 0 && result.Output != "" {
			result.Messages = []message.Message{{Role: message.RoleAssistant, Content: result.Output}}
		}
		toSave := result.Messages
		if result.Compacted {
			toSave = result.Transcript
			if toSave == nil {
				runErr = errors.New("compacted agent result is missing the complete transcript")
			}
		}
		if runErr == nil {
			if err := s.store.SaveHistory(persistCtx, run.ThreadID, toSave, result.Compacted); err != nil {
				runErr = fmt.Errorf("saving history: %w", err)
			}
		}
		if runErr == nil && len(result.Values) > 0 {
			if _, err := s.store.UpdateThread(persistCtx, run.ThreadID, nil, result.Values); err != nil {
				runErr = fmt.Errorf("saving thread state: %w", err)
			}
		}
		if runErr == nil {
			values, err := s.state(persistCtx, run.ThreadID)
			if err != nil {
				runErr = fmt.Errorf("loading final state: %w", err)
			} else {
				s.publish(persistCtx, run, "values", values["values"])
			}
		}
		if runErr == nil {
			for _, m := range result.Messages {
				if m.Role == message.RoleUser || m.Role == message.RoleSystem {
					continue
				}
				// Streaming runs have already projected model deltas, and the
				// authoritative values event above contains the complete turn.
				// Re-publishing tool/final messages here gives them fresh wire IDs
				// and makes clients render the same turn twice.
				if result.Streamed {
					continue
				}
				wm := toWireMessage(m)
				s.publish(persistCtx, run, "messages", []any{wm, map[string]any{"run_id": run.RunID, "thread_id": run.ThreadID}})
			}
			totals := journal.Totals()
			s.publish(persistCtx, run, "custom", tokenUsagePayload(totals.InputTokens, totals.OutputTokens, totals.CachedInputTokens, totals.LeadTokens, totals.SubagentTokens, totals.MiddlewareTokens))
		}
	}
	if runErr != nil {
		status = "error"
		if errors.Is(runErr, context.Canceled) {
			status = "cancelled"
		}
		s.publish(persistCtx, run, "error", map[string]any{"message": runErr.Error(), "type": fmt.Sprintf("%T", runErr)})
	}
	if _, err := s.store.UpdateRun(persistCtx, run.RunID, RunUpdate{Status: status, RiskLevel: result.RiskLevel}); err != nil {
		finalErr := fmt.Errorf("saving terminal run state: %w", err)
		if runErr == nil {
			runErr = finalErr
			status = "error"
			s.publish(persistCtx, run, "error", map[string]any{"message": finalErr.Error(), "type": fmt.Sprintf("%T", finalErr)})
		} else {
			s.logger.Error("updating terminal run state", "run_id", run.RunID, "error", err)
		}
	}
	totals := journal.Totals()
	completion := RunCompletion{
		RunID: run.RunID, ThreadID: run.ThreadID, Status: status,
		Iterations: result.Iterations, LLMCalls: totals.LLMCalls,
		InputTokens: totals.InputTokens, OutputTokens: totals.OutputTokens, CachedInputTokens: totals.CachedInputTokens,
		LeadTokens: totals.LeadTokens, SubagentTokens: totals.SubagentTokens,
		MiddlewareTokens: totals.MiddlewareTokens, CostMicros: totals.CostMicros,
		Duration: time.Since(startedAt), CompletedAt: time.Now().UTC(),
	}
	if err := s.store.SaveRunCompletion(persistCtx, completion); err != nil {
		finalErr := fmt.Errorf("saving run completion: %w", err)
		if runErr == nil {
			runErr = finalErr
			status = "error"
			s.publish(persistCtx, run, "error", map[string]any{"message": finalErr.Error(), "type": fmt.Sprintf("%T", finalErr)})
		} else {
			s.logger.Error("saving run completion", "run_id", run.RunID, "error", err)
		}
	} else {
		journal.SetOnChange(func(updated runtime.Totals) {
			late := completion
			late.LLMCalls = updated.LLMCalls
			late.InputTokens = updated.InputTokens
			late.OutputTokens = updated.OutputTokens
			late.CachedInputTokens = updated.CachedInputTokens
			late.LeadTokens = updated.LeadTokens
			late.SubagentTokens = updated.SubagentTokens
			late.MiddlewareTokens = updated.MiddlewareTokens
			late.CostMicros = updated.CostMicros
			if err := s.store.SaveRunCompletion(persistCtx, late); err != nil {
				s.logger.Error("saving late subagent usage", "run_id", run.RunID, "error", err)
			}
		})
	}
	runtimeStatus := runtime.StatusCompleted
	switch status {
	case "cancelled":
		runtimeStatus = runtime.StatusCancelled
	case "error":
		runtimeStatus = runtime.StatusFailed
	}
	_ = s.registry.Complete(persistCtx, run.RunID, runtimeStatus)
	s.publish(persistCtx, run, "end", runtime.RunEnd{Status: status, Output: result.Output, Iterations: result.Iterations, RiskLevel: result.RiskLevel, Error: errorString(runErr)})
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func tokenUsagePayload(input, output, cached, lead, subagent, middleware int) map[string]any {
	return map[string]any{
		"type":              "token_usage",
		"input_tokens":      input,
		"output_tokens":     output,
		"cache_read_tokens": cached,
		"total_tokens":      input + output,
		"lead_tokens":       lead,
		"subagent_tokens":   subagent,
		"middleware_tokens": middleware,
	}
}

func mergeMaps(sources ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, source := range sources {
		for key, value := range source {
			out[key] = value
		}
	}
	return out
}

func (s *Server) publish(ctx context.Context, run Run, name string, data any) {
	typ := wireEventType(name, data)
	e := runtime.MustEvent(run.RunID, run.ThreadID, typ, makeWireEvent(name, data))
	s.publishEvent(ctx, e)
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

func (s *Server) publishEvent(ctx context.Context, e runtime.Event) int64 {
	e.Seq = s.bus.Publish(ctx, e)
	s.persister.Persist(ctx, e)
	return e.Seq
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
		_ = writeSSE(w, 0, makeWireEvent("end", map[string]any{"status": "error", "error": "run interrupted because its worker stopped"}))
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
	updated, err := s.store.UpdateRun(ctx, run.RunID, RunUpdate{Status: "error"})
	if err != nil {
		return run, false, err
	}
	_ = s.registry.Complete(ctx, run.RunID, runtime.StatusFailed)
	s.logger.Warn("reconciled orphaned run", "run_id", run.RunID, "thread_id", run.ThreadID)
	return updated, true, nil
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
func (s *Server) threadStream(w http.ResponseWriter, r *http.Request) {
	runs, _ := s.store.ListRuns(r.Context(), r.PathValue("tid"))
	for i := len(runs) - 1; i >= 0; i-- {
		runs[i], _, _ = s.reconcileActiveRun(r.Context(), runs[i])
		if runs[i].Status == "running" || runs[i].Status == "pending" {
			r.SetPathValue("rid", runs[i].RunID)
			s.reconnectRun(w, r)
			return
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_ = writeSSE(w, 0, makeWireEvent("end", map[string]any{}))
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
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
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
