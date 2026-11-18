package service

import (
	"context"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/model"
)

func (s *Service) ListThreads(ctx context.Context, limit, offset int, archived bool) ([]model.Thread, int, error) {
	return s.repository.ListThreads(ctx, limit, offset, archived)
}

func (s *Service) CreateThread(ctx context.Context, id, title, name string) (model.Thread, error) {
	return s.repository.CreateThread(ctx, id, title, name)
}

func (s *Service) UpdateThread(ctx context.Context, id string, patch model.ThreadPatch) (model.Thread, error) {
	return s.repository.UpdateThread(ctx, id, patch)
}

func (s *Service) DeleteThread(ctx context.Context, id string) error {
	return s.repository.DeleteThread(ctx, id)
}
