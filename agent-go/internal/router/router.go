package router

import (
	"net/http"

	"github.com/KyrieWang7/nous-agent/platform-go/httpx"
	"github.com/gin-gonic/gin"
)

type Endpoints struct {
	Plugins, ChangePlugins, ReloadPlugins                                                                         http.HandlerFunc
	EnqueueInput, ListInputs, GetInput, CancelInput, ChangeInput                                                  http.HandlerFunc
	ListSubagents, GetSubagent, CancelSubagent, WaitSubagent, ContinueSubagent, SendSubagentInput, SubagentInputs http.HandlerFunc
	Health                                                                                                        http.HandlerFunc
	CreateThread                                                                                                  http.HandlerFunc
	SearchThreads                                                                                                 http.HandlerFunc
	GetThread                                                                                                     http.HandlerFunc
	PatchThread                                                                                                   http.HandlerFunc
	DeleteThread                                                                                                  http.HandlerFunc
	GetState                                                                                                      http.HandlerFunc
	PostState                                                                                                     http.HandlerFunc
	GetHistory                                                                                                    http.HandlerFunc
	CreateRun                                                                                                     http.HandlerFunc
	ListRuns                                                                                                      http.HandlerFunc
	GetRun                                                                                                        http.HandlerFunc
	ForkRun                                                                                                       http.HandlerFunc
	ReconnectRun                                                                                                  http.HandlerFunc
	RawEvents                                                                                                     http.HandlerFunc
	CancelRun                                                                                                     http.HandlerFunc
	GetQuestion                                                                                                   http.HandlerFunc
	AnswerQuestion                                                                                                http.HandlerFunc
}

func New(endpoints Endpoints, middleware ...gin.HandlerFunc) *gin.Engine {
	engine := httpx.New(middleware...)
	httpx.Handle(engine, "GET", "/ok", endpoints.Health)
	httpx.Handle(engine, "POST", "/api/v1/threads", endpoints.CreateThread)
	httpx.Handle(engine, "GET", "/api/v1/threads", endpoints.SearchThreads)
	httpx.Handle(engine, "GET", "/api/v1/threads/{tid}", endpoints.GetThread)
	httpx.Handle(engine, "PATCH", "/api/v1/threads/{tid}", endpoints.PatchThread)
	httpx.Handle(engine, "DELETE", "/api/v1/threads/{tid}", endpoints.DeleteThread)
	httpx.Handle(engine, "GET", "/api/v1/threads/{tid}/state", endpoints.GetState)
	httpx.Handle(engine, "POST", "/api/v1/threads/{tid}/state", endpoints.PostState)
	httpx.Handle(engine, "GET", "/api/v1/threads/{tid}/history", endpoints.GetHistory)
	httpx.Handle(engine, "POST", "/api/v1/threads/{tid}/runs", endpoints.CreateRun)
	httpx.Handle(engine, "GET", "/api/v1/threads/{tid}/runs", endpoints.ListRuns)
	httpx.Handle(engine, "GET", "/api/v1/threads/{tid}/runs/{rid}", endpoints.GetRun)
	httpx.Handle(engine, "POST", "/api/v1/threads/{tid}/runs/{rid}/fork", endpoints.ForkRun)
	httpx.Handle(engine, "GET", "/api/v1/threads/{tid}/runs/{rid}/events", endpoints.ReconnectRun)
	httpx.Handle(engine, "GET", "/api/v1/threads/{tid}/runs/{rid}/events/raw", endpoints.RawEvents)
	httpx.Handle(engine, "POST", "/api/v1/threads/{tid}/runs/{rid}/cancel", endpoints.CancelRun)
	httpx.Handle(engine, "GET", "/api/v1/threads/{tid}/runs/{rid}/questions/{qid}", endpoints.GetQuestion)
	httpx.Handle(engine, "POST", "/api/v1/threads/{tid}/runs/{rid}/questions/{qid}/answer", endpoints.AnswerQuestion)
	httpx.Handle(engine, "GET", "/api/v1/threads/{tid}/subagents", endpoints.ListSubagents)
	httpx.Handle(engine, "GET", "/api/v1/threads/{tid}/subagents/{sid}", endpoints.GetSubagent)
	httpx.Handle(engine, "POST", "/api/v1/threads/{tid}/subagents/{sid}/cancel", endpoints.CancelSubagent)
	httpx.Handle(engine, "POST", "/api/v1/threads/{tid}/subagents/{sid}/wait", endpoints.WaitSubagent)
	httpx.Handle(engine, "POST", "/api/v1/threads/{tid}/subagents/{sid}/continue", endpoints.ContinueSubagent)
	httpx.Handle(engine, "GET", "/api/v1/threads/{tid}/subagents/{sid}/messages", endpoints.SubagentInputs)
	httpx.Handle(engine, "POST", "/api/v1/threads/{tid}/subagents/{sid}/messages", endpoints.SendSubagentInput)
	httpx.Handle(engine, "POST", "/api/v1/threads/{tid}/messages", endpoints.EnqueueInput)
	httpx.Handle(engine, "GET", "/api/v1/threads/{tid}/messages", endpoints.ListInputs)
	httpx.Handle(engine, "GET", "/api/v1/threads/{tid}/messages/{mid}", endpoints.GetInput)
	httpx.Handle(engine, "PATCH", "/api/v1/threads/{tid}/messages/{mid}", endpoints.ChangeInput)
	httpx.Handle(engine, "POST", "/api/v1/threads/{tid}/messages/{mid}/cancel", endpoints.CancelInput)
	httpx.Handle(engine, "GET", "/api/v1/runtime/plugins", endpoints.Plugins)
	httpx.Handle(engine, "POST", "/api/v1/runtime/plugins", endpoints.ChangePlugins)
	httpx.Handle(engine, "POST", "/api/v1/runtime/plugins/reload", endpoints.ReloadPlugins)
	return engine
}
