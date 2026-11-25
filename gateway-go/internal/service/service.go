// Package service owns control-plane use cases and depends on storage contracts.
package service

import (
	"context"
	"mime/multipart"
	"os"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/model"
	"github.com/KyrieWang7/nous-agent/platform-go/modelconfig"
)

type Catalog interface {
	ModelSettings() (modelconfig.PublicSettings, error)
	SaveModelSettings(modelconfig.Input) (modelconfig.PublicSettings, error)
	Models() ([]model.Model, error)
	Extensions() (model.Extensions, error)
	SaveMCP(map[string]model.MCPServer) (model.Extensions, error)
	Skills() ([]model.Skill, error)
	Skill(string) (model.Skill, error)
	SetSkillEnabled(string, bool) (model.Skill, error)
}
type Files interface {
	InstallSkill(string, string) (string, error)
	SaveUploads(context.Context, string, []*multipart.FileHeader) ([]model.Info, error)
	ListUploads(string) ([]model.Info, error)
	DeleteUpload(string, string) error
	OpenArtifact(string, string) (*os.File, error)
	Workspace(context.Context, string, string, string) (model.WorkspaceListing, error)
}
type Repository interface {
	Available() bool
	ListThreads(context.Context, int, int, bool) ([]model.Thread, int, error)
	CreateThread(context.Context, string, string, string) (model.Thread, error)
	UpdateThread(context.Context, string, model.ThreadPatch) (model.Thread, error)
	DeleteThread(context.Context, string) error
	Facts(context.Context) ([]model.Fact, error)
}
type SwarmQueries interface {
	Teams(context.Context, string) ([]model.Team, error)
	Members(context.Context, string) ([]model.Member, error)
	Messages(context.Context, string, int64, int) ([]model.Message, error)
	TeamExists(context.Context, string) (bool, error)
}
type Options struct {
	Catalog    Catalog
	Files      Files
	Repository Repository
	Swarm      SwarmQueries
}
type Service struct {
	catalog    Catalog
	files      Files
	repository Repository
	swarm      SwarmQueries
}

func New(opts Options) *Service {
	return &Service{catalog: opts.Catalog, files: opts.Files, repository: opts.Repository, swarm: opts.Swarm}
}
