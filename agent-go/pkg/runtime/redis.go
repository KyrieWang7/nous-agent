package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisCancelChannel = "nous-agent:runs:cancel"

type RedisRegistryBackend struct {
	client redis.UniversalClient
	prefix string
}

func NewRedisRegistryBackend(client redis.UniversalClient) *RedisRegistryBackend {
	return &RedisRegistryBackend{client: client, prefix: "nous-agent:run:"}
}
func (r *RedisRegistryBackend) Save(ctx context.Context, rec RunRecord, ttl time.Duration) error {
	if r.client == nil {
		return errors.New("runtime: redis client is nil")
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return r.client.Set(ctx, r.prefix+rec.RunID, raw, ttl).Err()
}
func (r *RedisRegistryBackend) Load(ctx context.Context, id string) (RunRecord, error) {
	raw, err := r.client.Get(ctx, r.prefix+id).Bytes()
	if errors.Is(err, redis.Nil) {
		return RunRecord{}, fmt.Errorf("%w: %q", ErrRunNotFound, id)
	}
	if err != nil {
		return RunRecord{}, err
	}
	var rec RunRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return RunRecord{}, err
	}
	return rec, nil
}
func (r *RedisRegistryBackend) Delete(ctx context.Context, id string) error {
	return r.client.Del(ctx, r.prefix+id).Err()
}
func (r *RedisRegistryBackend) BroadcastCancel(ctx context.Context, id string) error {
	return r.client.Publish(ctx, redisCancelChannel, id).Err()
}
func (r *RedisRegistryBackend) SubscribeCancel(ctx context.Context) (<-chan string, func(), error) {
	pub := r.client.Subscribe(ctx, redisCancelChannel)
	if _, err := pub.Receive(ctx); err != nil {
		_ = pub.Close()
		return nil, nil, err
	}
	out := make(chan string, 64)
	stopCtx, cancel := context.WithCancel(ctx)
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); _ = pub.Close() }) }
	go func() {
		defer close(out)
		ch := pub.Channel()
		for {
			select {
			case <-stopCtx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				select {
				case out <- msg.Payload:
				case <-stopCtx.Done():
					return
				}
			}
		}
	}()
	return out, stop, nil
}

type RedisEventStore struct {
	client redis.UniversalClient
	prefix string
	ttl    time.Duration
}

func NewRedisEventStore(client redis.UniversalClient, ttl time.Duration) *RedisEventStore {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &RedisEventStore{client: client, prefix: "nous-agent:events:", ttl: ttl}
}
func (s *RedisEventStore) PutBatch(ctx context.Context, events []Event) error {
	if s.client == nil {
		return errors.New("runtime: redis client is nil")
	}
	pipe := s.client.Pipeline()
	keys := map[string]struct{}{}
	for _, e := range events {
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		key := s.prefix + e.RunID
		keys[key] = struct{}{}
		pipe.XAdd(ctx, &redis.XAddArgs{Stream: key, ID: fmt.Sprintf("%d-1", e.Seq), Values: map[string]any{"event": raw}})
	}
	for key := range keys {
		pipe.Expire(ctx, key, s.ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}
func (s *RedisEventStore) Get(ctx context.Context, runID string, afterSeq int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 200
	}
	start := "-"
	if afterSeq > 0 {
		start = fmt.Sprintf("(%d-1", afterSeq)
	}
	rows, err := s.client.XRangeN(ctx, s.prefix+runID, start, "+", int64(limit)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, row := range rows {
		v, ok := row.Values["event"]
		if !ok {
			continue
		}
		var raw []byte
		switch x := v.(type) {
		case string:
			raw = []byte(x)
		case []byte:
			raw = x
		default:
			raw = []byte(fmt.Sprint(x))
		}
		var e Event
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, err
		}
		if e.Seq == 0 {
			e.Seq = parseRedisSeq(row.ID)
		}
		out = append(out, e)
	}
	return out, nil
}
func parseRedisSeq(id string) int64 {
	head, _, _ := strings.Cut(id, "-")
	n, _ := strconv.ParseInt(head, 10, 64)
	return n
}

// MultiEventStore writes to every store and reads from Primary. Mirror write
// failures are returned so the async persister logs the degraded channel.
type MultiEventStore struct {
	Primary EventStore
	Mirrors []EventStore
}

func (m MultiEventStore) PutBatch(ctx context.Context, events []Event) error {
	if m.Primary == nil {
		return errors.New("runtime: primary event store is nil")
	}
	if err := m.Primary.PutBatch(ctx, events); err != nil {
		return err
	}
	for _, s := range m.Mirrors {
		if err := s.PutBatch(ctx, events); err != nil {
			return err
		}
	}
	return nil
}
func (m MultiEventStore) Get(ctx context.Context, runID string, after int64, limit int) ([]Event, error) {
	if m.Primary == nil {
		return nil, errors.New("runtime: primary event store is nil")
	}
	return m.Primary.Get(ctx, runID, after, limit)
}
