package redis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	goredis "github.com/redis/go-redis/v9"
)

const cancelChannel = "nous-agent:runs:cancel"

type RegistryBackend struct {
	client goredis.UniversalClient
	prefix string
}

func NewRegistryBackend(client goredis.UniversalClient) *RegistryBackend {
	return &RegistryBackend{client: client, prefix: "nous-agent:run:"}
}

func (r *RegistryBackend) Save(ctx context.Context, rec runtime.RunRecord, ttl time.Duration) error {
	if r.client == nil {
		return errors.New("runtime/redis: redis client is nil")
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return r.client.Set(ctx, r.prefix+rec.RunID, raw, ttl).Err()
}

func (r *RegistryBackend) Load(ctx context.Context, id string) (runtime.RunRecord, error) {
	raw, err := r.client.Get(ctx, r.prefix+id).Bytes()
	if errors.Is(err, goredis.Nil) {
		return runtime.RunRecord{}, fmt.Errorf("%w: %q", runtime.ErrRunNotFound, id)
	}
	if err != nil {
		return runtime.RunRecord{}, err
	}
	var rec runtime.RunRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return runtime.RunRecord{}, err
	}
	return rec, nil
}

func (r *RegistryBackend) Delete(ctx context.Context, id string) error {
	return r.client.Del(ctx, r.prefix+id).Err()
}

func (r *RegistryBackend) BroadcastCancel(ctx context.Context, id string) error {
	return r.client.Publish(ctx, cancelChannel, id).Err()
}

func (r *RegistryBackend) SubscribeCancel(ctx context.Context) (<-chan string, func(), error) {
	pub := r.client.Subscribe(ctx, cancelChannel)
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
		messages := pub.Channel()
		for {
			select {
			case <-stopCtx.Done():
				return
			case message, ok := <-messages:
				if !ok {
					return
				}
				select {
				case out <- message.Payload:
				case <-stopCtx.Done():
					return
				}
			}
		}
	}()
	return out, stop, nil
}

type EventStore struct {
	client goredis.UniversalClient
	prefix string
	ttl    time.Duration
}

func NewEventStore(client goredis.UniversalClient, ttl time.Duration) *EventStore {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &EventStore{client: client, prefix: "nous-agent:events:", ttl: ttl}
}

func (s *EventStore) PutBatch(ctx context.Context, events []runtime.Event) error {
	if s.client == nil {
		return errors.New("runtime/redis: redis client is nil")
	}
	pipe := s.client.Pipeline()
	keys := map[string]struct{}{}
	type pendingAdd struct {
		event runtime.Event
		key   string
		id    string
		raw   []byte
		cmd   *goredis.StringCmd
	}
	adds := make([]pendingAdd, 0, len(events))
	for _, event := range events {
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		key := s.prefix + event.RunID
		id := fmt.Sprintf("%d-1", event.Seq)
		keys[key] = struct{}{}
		cmd := pipe.XAdd(ctx, &goredis.XAddArgs{Stream: key, ID: id, Values: map[string]any{"event": raw}})
		adds = append(adds, pendingAdd{event: event, key: key, id: id, raw: raw, cmd: cmd})
	}
	expires := make([]*goredis.BoolCmd, 0, len(keys))
	for key := range keys {
		expires = append(expires, pipe.Expire(ctx, key, s.ttl))
	}
	_, execErr := pipe.Exec(ctx)
	if execErr == nil {
		return nil
	}
	for _, add := range adds {
		if add.cmd.Err() == nil {
			continue
		}
		if !strings.Contains(add.cmd.Err().Error(), "equal or smaller") {
			return fmt.Errorf("runtime/redis: appending run %q seq %d: %w", add.event.RunID, add.event.Seq, add.cmd.Err())
		}
		rows, err := s.client.XRangeN(ctx, add.key, add.id, add.id, 1).Result()
		if err != nil || len(rows) != 1 || !bytes.Equal(eventBytes(rows[0].Values["event"]), add.raw) {
			return fmt.Errorf("runtime/redis: event sequence conflict for run %q seq %d: %w", add.event.RunID, add.event.Seq, add.cmd.Err())
		}
	}
	for _, expire := range expires {
		if err := expire.Err(); err != nil {
			return fmt.Errorf("runtime/redis: expiring event stream: %w", err)
		}
	}
	return nil
}

func (s *EventStore) Get(ctx context.Context, runID string, afterSeq int64, limit int) ([]runtime.Event, error) {
	if limit <= 0 {
		limit = 200
	}
	start := "-"
	if afterSeq > 0 {
		start = fmt.Sprintf("(%d-1", afterSeq)
	}
	rows, err := s.client.XRangeN(ctx, s.prefix+runID, start, "+", int64(limit)).Result()
	if errors.Is(err, goredis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	events := make([]runtime.Event, 0, len(rows))
	for _, row := range rows {
		value, ok := row.Values["event"]
		if !ok {
			continue
		}
		raw := eventBytes(value)
		var event runtime.Event
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, err
		}
		if event.Seq == 0 {
			event.Seq = parseSeq(row.ID)
		}
		events = append(events, event)
	}
	return events, nil
}

func eventBytes(value any) []byte {
	switch value := value.(type) {
	case string:
		return []byte(value)
	case []byte:
		return value
	default:
		return []byte(fmt.Sprint(value))
	}
}

func (s *EventStore) LastSeq(ctx context.Context, runID string) (int64, error) {
	rows, err := s.client.XRevRangeN(ctx, s.prefix+runID, "+", "-", 1).Result()
	if errors.Is(err, goredis.Nil) || len(rows) == 0 {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return parseSeq(rows[0].ID), nil
}

func parseSeq(id string) int64 {
	head, _, _ := strings.Cut(id, "-")
	seq, _ := strconv.ParseInt(head, 10, 64)
	return seq
}

var (
	_ runtime.RegistryBackend     = (*RegistryBackend)(nil)
	_ runtime.EventStore          = (*EventStore)(nil)
	_ runtime.EventSequenceReader = (*EventStore)(nil)
)
