package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func redisClient(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return mr, client
}
func TestRedisEventStoreCursorAndTTL(t *testing.T) {
	mr, c := redisClient(t)
	store := runtime.NewRedisEventStore(c, time.Hour)
	ctx := context.Background()
	var events []runtime.Event
	for i := int64(1); i <= 3; i++ {
		e := runtime.MustEvent("r", "t", runtime.EventContentDelta, map[string]int64{"n": i})
		e.Seq = i
		events = append(events, e)
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
func TestRedisRegistryCancelCrossesInstances(t *testing.T) {
	_, c := redisClient(t)
	backend := runtime.NewRedisRegistryBackend(c)
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
