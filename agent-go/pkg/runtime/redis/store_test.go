package redis_test

import (
	"context"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	runtimeredis "github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/redis"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/subagent"
	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func redisClient(t *testing.T) (*miniredis.Miniredis, *goredis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return mr, client
}

func TestTaskStoreSharesStatusAcrossInstances(t *testing.T) {
	server := miniredis.RunT(t)
	clientA := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	clientB := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })
	storeA, err := runtimeredis.NewTaskStore(clientA)
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := runtimeredis.NewTaskStore(clientB)
	if err != nil {
		t.Fatal(err)
	}
	completedAt := time.Now().UTC().Truncate(time.Nanosecond)
	want := subagent.Result{
		TaskID: "call-cross-instance", SubagentType: "verification", Status: message.SubagentCompleted,
		Output: "verified", StartedAt: completedAt.Add(-time.Second), CompletedAt: &completedAt,
		Usage: model.Usage{InputTokens: 7, OutputTokens: 3},
	}
	if err := storeA.Put(context.Background(), want, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := storeB.Get(context.Background(), want.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TaskID != want.TaskID || got.SubagentType != want.SubagentType || got.Status != want.Status || got.Output != want.Output || got.Usage != want.Usage || !got.StartedAt.Equal(want.StartedAt) || got.CompletedAt == nil || !got.CompletedAt.Equal(completedAt) {
		t.Fatalf("cross-instance result = %#v, want %#v", got, want)
	}
}

func TestEventStoreCursorAndTTL(t *testing.T) {
	mr, client := redisClient(t)
	store := runtimeredis.NewEventStore(client, time.Hour)
	ctx := context.Background()
	var events []runtime.Event
	for seq := int64(1); seq <= 3; seq++ {
		event := runtime.MustEvent("r", "t", runtime.EventContentDelta, map[string]int64{"n": seq})
		event.Seq = seq
		events = append(events, event)
	}
	if err := store.PutBatch(ctx, events); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "r", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Seq != 2 || got[1].Seq != 3 {
		t.Fatalf("events=%#v", got)
	}
	if ttl := mr.TTL("nous-agent:events:r"); ttl <= 0 {
		t.Fatalf("ttl=%s", ttl)
	}
}

func TestEventStorePutBatchIsIdempotentForIdenticalSequence(t *testing.T) {
	_, client := redisClient(t)
	store := runtimeredis.NewEventStore(client, time.Hour)
	event := runtime.MustEvent("r", "t", runtime.EventToolStart, map[string]string{"name": "bash"})
	event.Seq = 1
	event.IdempotencyKey = "tool:1:start"
	if err := store.PutBatch(context.Background(), []runtime.Event{event}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutBatch(context.Background(), []runtime.Event{event}); err != nil {
		t.Fatalf("identical retry failed: %v", err)
	}
	got, err := store.Get(context.Background(), "r", 0, 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("events=%+v err=%v", got, err)
	}
}

func TestEventStoreRejectsConflictingSequence(t *testing.T) {
	_, client := redisClient(t)
	store := runtimeredis.NewEventStore(client, time.Hour)
	first := runtime.MustEvent("r", "t", runtime.EventToolStart, nil)
	first.Seq = 1
	conflict := runtime.MustEvent("r", "t", runtime.EventToolResult, nil)
	conflict.Seq = 1
	if err := store.PutBatch(context.Background(), []runtime.Event{first}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutBatch(context.Background(), []runtime.Event{conflict}); err == nil {
		t.Fatal("conflicting event sequence was accepted")
	}
}

func TestEventPublisherCommitsQueuedTraceBeforeAudit(t *testing.T) {
	_, client := redisClient(t)
	store := runtimeredis.NewEventStore(client, time.Hour)
	publisher, err := runtime.NewEventPublisher(runtime.NewMemoryBus(runtime.BusOptions{}), store, runtime.PersisterOptions{
		BatchSize: 100, FlushInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	if _, err := publisher.Publish(context.Background(), runtime.MustEvent("r", "t", runtime.EventContentDelta, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.Publish(context.Background(), runtime.MustEvent("r", "t", runtime.EventRunEnd, nil)); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), "r", 0, 10)
	if err != nil || len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 2 {
		t.Fatalf("events=%+v err=%v", got, err)
	}
}

func TestRegistryCancelCrossesInstances(t *testing.T) {
	_, client := redisClient(t)
	backend := runtimeredis.NewRegistryBackend(client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	holder, err := runtime.NewRegistry(ctx, runtime.RegistryOptions{Backend: backend, Owner: "a", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	other, err := runtime.NewRegistry(ctx, runtime.RegistryOptions{Backend: backend, Owner: "b", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	runCtx, err := holder.Register(ctx, runtime.RunRecord{RunID: "r", ThreadID: "t", OnDisconnect: runtime.DisconnectContinue})
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Cancel(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("cancel did not cross instances")
	}
}
