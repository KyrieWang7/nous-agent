package service

import (
	"context"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/model"
)

func (s *Service) Available() bool { return s.repository.Available() }

func (s *Service) Facts(ctx context.Context) ([]model.Fact, error) { return s.repository.Facts(ctx) }

func (s *Service) Teams(ctx context.Context, tid string) ([]model.Team, error) {
	return s.swarm.Teams(ctx, tid)
}

func (s *Service) Members(ctx context.Context, tid string) ([]model.Member, error) {
	return s.swarm.Members(ctx, tid)
}

func (s *Service) Messages(ctx context.Context, tid string, after int64, limit int) ([]model.Message, error) {
	return s.swarm.Messages(ctx, tid, after, limit)
}

func (s *Service) TeamExists(ctx context.Context, tid string) (bool, error) {
	return s.swarm.TeamExists(ctx, tid)
}
