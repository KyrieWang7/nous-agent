package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

func (s *Service) PublishEvent(ctx context.Context, event runtime.Event) (int64, error) {
	return s.publishEvent(ctx, event)
}
func (s *Service) Subscribe(rid string) (<-chan runtime.Event, func()) { return s.bus.Subscribe(rid) }
func (s *Service) Backlog(rid string, after int64) []runtime.Event     { return s.bus.Backlog(rid, after) }
func (s *Service) Replay(ctx context.Context, rid string, after int64, yield func(runtime.Event) bool) error {
	return runtime.Replay(ctx, s.events, rid, after, 200, yield)
}
func (s *Service) ClientDisconnected(ctx context.Context, rid string) error {
	return s.registry.ClientDisconnected(ctx, rid, s.bus.SubscriberCount(rid))
}
func (s *Service) Heartbeat() time.Duration { return s.heartbeat }
func (s *Service) Logger() *slog.Logger     { return s.logger }
