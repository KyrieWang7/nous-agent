package service

import (
	"context"
	"mime/multipart"
	"os"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/model"
)

func (s *Service) InstallSkill(tid, path string) (string, error) {
	return s.files.InstallSkill(tid, path)
}

func (s *Service) SaveUploads(ctx context.Context, tid string, headers []*multipart.FileHeader) ([]model.Info, error) {
	return s.files.SaveUploads(ctx, tid, headers)
}

func (s *Service) ListUploads(tid string) ([]model.Info, error) { return s.files.ListUploads(tid) }

func (s *Service) DeleteUpload(tid, name string) error { return s.files.DeleteUpload(tid, name) }

func (s *Service) OpenArtifact(tid, path string) (*os.File, error) {
	return s.files.OpenArtifact(tid, path)
}

func (s *Service) Workspace(ctx context.Context, tid, path, query string) (model.WorkspaceListing, error) {
	return s.files.Workspace(ctx, tid, path, query)
}
