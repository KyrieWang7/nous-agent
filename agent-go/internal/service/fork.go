package service

import (
	"context"
	"errors"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/replay"
)

var ErrForkUnavailable = errors.New("this run has no available completed transcript")

type forkStore interface {
	CreateFork(context.Context, model.Thread, string, string, string, []message.Message) (model.Thread, error)
}

func (s *Service) ForkRun(ctx context.Context, tid, rid, destination string) (model.Thread, error) {
	store, ok := s.store.(forkStore)
	if !ok {
		return model.Thread{}, ErrForkUnavailable
	}
	run, err := s.ownedRun(ctx, tid, rid)
	if err != nil {
		return model.Thread{}, err
	}
	if run.Status != "success" {
		return model.Thread{}, ErrForkUnavailable
	}
	snapshot, err := s.snapshots.Load(ctx, rid, tid)
	if errors.Is(err, replay.ErrSnapshotNotFound) {
		return model.Thread{}, ErrForkUnavailable
	}
	if err != nil {
		return model.Thread{}, err
	}
	if snapshot.RunID != rid || snapshot.ThreadID != tid || len(snapshot.Messages) == 0 {
		return model.Thread{}, ErrForkUnavailable
	}
	source, err := s.store.GetThread(ctx, tid)
	if err != nil {
		return model.Thread{}, err
	}
	title, _ := source.Values["title"].(string)
	if title != "" {
		title = "Fork · " + title
	} else {
		title = "Forked conversation"
	}
	target := model.NewThread(destination, map[string]any{
		"forked_from_thread_id": tid,
		"forked_from_run_id":    rid,
	})
	return store.CreateFork(ctx, target, tid, rid, title, snapshot.Messages)
}
