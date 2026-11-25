package router

import (
	"net/http"

	"github.com/KyrieWang7/nous-agent/platform-go/httpx"
	"github.com/gin-gonic/gin"
)

type Endpoints struct {
	GetModelSettings, PutModelSettings http.HandlerFunc
	Health                             http.HandlerFunc
	Models                             http.HandlerFunc
	Model                              http.HandlerFunc
	GetMCP                             http.HandlerFunc
	PutMCP                             http.HandlerFunc
	Skills                             http.HandlerFunc
	Skill                              http.HandlerFunc
	PutSkill                           http.HandlerFunc
	InstallSkill                       http.HandlerFunc
	Threads                            http.HandlerFunc
	CreateThread                       http.HandlerFunc
	UpdateThread                       http.HandlerFunc
	DeleteThread                       http.HandlerFunc
	Upload                             http.HandlerFunc
	Uploads                            http.HandlerFunc
	DeleteUpload                       http.HandlerFunc
	Artifact                           http.HandlerFunc
	WorkspaceFiles                     http.HandlerFunc
	Memory                             http.HandlerFunc
	MemoryConfig                       http.HandlerFunc
	MemoryStatus                       http.HandlerFunc
	Teams                              http.HandlerFunc
	Members                            http.HandlerFunc
	SwarmStream                        http.HandlerFunc
}

func New(endpoints Endpoints, middleware ...gin.HandlerFunc) *gin.Engine {
	engine := httpx.New(middleware...)
	httpx.Handle(engine, "GET", "/health", endpoints.Health)
	httpx.Handle(engine, "GET", "/api/models", endpoints.Models)
	httpx.Handle(engine, "GET", "/api/models/config", endpoints.GetModelSettings)
	httpx.Handle(engine, "PUT", "/api/models/config", endpoints.PutModelSettings)
	httpx.Handle(engine, "GET", "/api/models/{name}", endpoints.Model)
	httpx.Handle(engine, "GET", "/api/mcp/config", endpoints.GetMCP)
	httpx.Handle(engine, "PUT", "/api/mcp/config", endpoints.PutMCP)
	httpx.Handle(engine, "GET", "/api/skills", endpoints.Skills)
	httpx.Handle(engine, "GET", "/api/skills/{name}", endpoints.Skill)
	httpx.Handle(engine, "PUT", "/api/skills/{name}", endpoints.PutSkill)
	httpx.Handle(engine, "POST", "/api/skills/install", endpoints.InstallSkill)
	httpx.Handle(engine, "GET", "/api/threads", endpoints.Threads)
	httpx.Handle(engine, "POST", "/api/threads", endpoints.CreateThread)
	httpx.Handle(engine, "PATCH", "/api/threads/{id}", endpoints.UpdateThread)
	httpx.Handle(engine, "DELETE", "/api/threads/{id}", endpoints.DeleteThread)
	httpx.Handle(engine, "POST", "/api/threads/{id}/uploads", endpoints.Upload)
	httpx.Handle(engine, "GET", "/api/threads/{id}/uploads/list", endpoints.Uploads)
	httpx.Handle(engine, "DELETE", "/api/threads/{id}/uploads/{name}", endpoints.DeleteUpload)
	httpx.Handle(engine, "GET", "/api/threads/{id}/artifacts/{path...}", endpoints.Artifact)
	httpx.Handle(engine, "GET", "/api/threads/{id}/files", endpoints.WorkspaceFiles)
	httpx.Handle(engine, "GET", "/api/memory", endpoints.Memory)
	httpx.Handle(engine, "POST", "/api/memory/reload", endpoints.Memory)
	httpx.Handle(engine, "GET", "/api/memory/config", endpoints.MemoryConfig)
	httpx.Handle(engine, "GET", "/api/memory/status", endpoints.MemoryStatus)
	httpx.Handle(engine, "GET", "/api/swarm/teams", endpoints.Teams)
	httpx.Handle(engine, "GET", "/api/swarm/teams/{id}/members", endpoints.Members)
	httpx.Handle(engine, "GET", "/api/swarm/teams/{id}/stream", endpoints.SwarmStream)
	return engine
}
