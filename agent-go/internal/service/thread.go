package service

import (
	"context"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
)

// ThreadState contains domain data; HTTP message and usage projections belong to handler.
type ThreadState struct {
	Thread        model.Thread
	History       []message.Message
	Completion    model.RunCompletion
	HasCompletion bool
}

func (s *Service) CreateThread(ctx context.Context, id string, metadata map[string]any, keep bool) (model.Thread, error) {
	if id == "" {
		id = model.NewID()
	}
	return s.store.CreateThread(ctx, model.NewThread(id, metadata), keep)
}
func (s *Service) SearchThreads(ctx context.Context) ([]model.Thread, error) {
	return s.store.SearchThreads(ctx)
}
func (s *Service) GetThread(ctx context.Context, id string) (model.Thread, error) {
	return s.store.GetThread(ctx, id)
}
func (s *Service) UpdateThread(ctx context.Context, id string, metadata, values map[string]any) (model.Thread, error) {
	if values != nil {
		lease, err := s.store.AcquireThread(ctx, id)
		if err != nil {
			return model.Thread{}, err
		}
		defer func() {
			if err := lease.Release(); err != nil {
				s.logger.Error("releasing thread mutation lease", "error", err)
			}
		}()
	}
	return s.store.UpdateThread(ctx, id, metadata, values)
}
func (s *Service) DeleteThread(ctx context.Context, id string) error {
	lease, err := s.store.AcquireThread(ctx, id)
	if err != nil {
		return err
	}
	defer func() {
		if err := lease.Release(); err != nil {
			s.logger.Error("releasing thread mutation lease", "error", err)
		}
	}()
	return s.store.DeleteThread(ctx, id)
}
func (s *Service) State(ctx context.Context, id string) (ThreadState, error) {
	var state ThreadState
	var err error
	if state.Thread, err = s.store.GetThread(ctx, id); err != nil {
		return state, err
	}
	if state.History, err = s.store.LoadHistory(ctx, id); err != nil {
		return state, err
	}
	state.Completion, state.HasCompletion, err = s.store.LatestRunCompletion(ctx, id)
	return state, err
}
