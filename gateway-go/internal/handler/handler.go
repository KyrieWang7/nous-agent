package handler

import (
	"time"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/router"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/service"
)

type Handler struct {
	app                    *service.Service
	swarm                  service.SwarmQueries
	swarmPollInterval      time.Duration
	swarmHeartbeatInterval time.Duration
}

func New(app *service.Service) *Handler {
	return &Handler{app: app, swarm: app, swarmPollInterval: time.Second, swarmHeartbeatInterval: 15 * time.Second}
}
func (s *Handler) Endpoints() router.Endpoints {
	return router.Endpoints{
		Health:         s.health,
		Models:         s.models,
		Model:          s.model,
		GetMCP:         s.getMCP,
		PutMCP:         s.putMCP,
		Skills:         s.skills,
		Skill:          s.skill,
		PutSkill:       s.putSkill,
		InstallSkill:   s.installSkill,
		Threads:        s.threads,
		CreateThread:   s.createThread,
		UpdateThread:   s.updateThread,
		DeleteThread:   s.deleteThread,
		Upload:         s.upload,
		Uploads:        s.uploads,
		DeleteUpload:   s.deleteUpload,
		Artifact:       s.artifact,
		WorkspaceFiles: s.workspaceFiles,
		Memory:         s.memory,
		MemoryConfig:   s.memoryConfig,
		MemoryStatus:   s.memoryStatus,
		Teams:          s.teams,
		Members:        s.members,
		SwarmStream:    s.swarmStream,
	}
}
