package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/subagent"
	goredis "github.com/redis/go-redis/v9"
)

type TaskStore struct {
	client goredis.UniversalClient
	prefix string
}

func NewTaskStore(client goredis.UniversalClient) (*TaskStore, error) {
	if client == nil {
		return nil, errors.New("runtime/redis: redis client is nil")
	}
	return &TaskStore{client: client, prefix: "nous-agent:task:"}, nil
}

func (s *TaskStore) Put(ctx context.Context, result subagent.Result, ttl time.Duration) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("runtime/redis: encoding task result: %w", err)
	}
	if err := s.client.Set(ctx, s.prefix+result.TaskID, raw, ttl).Err(); err != nil {
		return fmt.Errorf("runtime/redis: saving task result: %w", err)
	}
	return nil
}

func (s *TaskStore) Get(ctx context.Context, id string) (subagent.Result, error) {
	raw, err := s.client.Get(ctx, s.prefix+id).Bytes()
	if err != nil {
		return subagent.Result{}, fmt.Errorf("runtime/redis: loading task result: %w", err)
	}
	var result subagent.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		return subagent.Result{}, fmt.Errorf("runtime/redis: decoding task result: %w", err)
	}
	return result, nil
}

var _ subagent.TaskStore = (*TaskStore)(nil)
