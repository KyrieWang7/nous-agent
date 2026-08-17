package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

// storeFactory 让契约测试跑在任意 EventStore 实现上。
//
// SQL 实现落地后只需在 stores() 里加一行，同一份契约立刻套用 —— 这是
// 防止"内存实现与 SQL 实现语义漂移"的唯一有效手段。
type storeFactory struct {
	name string
	make func(t *testing.T) runtime.EventStore
}

func stores() []storeFactory {
	return []storeFactory{
		{
			name: "memory",
			make: func(*testing.T) runtime.EventStore { return runtime.NewMemoryEventStore() },
		},
	}
}

func seed(t *testing.T, s runtime.EventStore, runID string, n int) {
	t.Helper()

	events := make([]runtime.Event, n)
	for i := range n {
		e := runtime.MustEvent(runID, "t1", runtime.EventContentDelta, map[string]int{"i": i})
		e.Seq = int64(i + 1)
		events[i] = e
	}
	if err := s.PutBatch(context.Background(), events); err != nil {
		t.Fatalf("PutBatch() error = %v", err)
	}
}

// --- EventStore 契约 ---

func TestEventStore_Contract(t *testing.T) {
	t.Parallel()

	for _, f := range stores() {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()

			t.Run("round trip", func(t *testing.T) {
				s := f.make(t)
				seed(t, s, "r1", 3)

				got, err := s.Get(context.Background(), "r1", 0, 100)
				if err != nil {
					t.Fatalf("Get() error = %v", err)
				}
				if len(got) != 3 {
					t.Fatalf("Get() = %d events, want 3", len(got))
				}
			})

			// 忽略 afterSeq 的实现会让分页变成无限循环。
			t.Run("cursor is honoured", func(t *testing.T) {
				s := f.make(t)
				seed(t, s, "r1", 10)

				got, err := s.Get(context.Background(), "r1", 7, 100)
				if err != nil {
					t.Fatalf("Get() error = %v", err)
				}
				if len(got) != 3 {
					t.Fatalf("Get(afterSeq=7) = %d events, want 3", len(got))
				}
				if got[0].Seq != 8 {
					t.Fatalf("first event seq = %d, want 8; the cursor was ignored", got[0].Seq)
				}
			})

			t.Run("results are ordered by seq", func(t *testing.T) {
				s := f.make(t)
				seed(t, s, "r1", 20)

				got, _ := s.Get(context.Background(), "r1", 0, 100)
				for i := 1; i < len(got); i++ {
					if got[i].Seq <= got[i-1].Seq {
						t.Fatalf("events out of order at %d: %d then %d", i, got[i-1].Seq, got[i].Seq)
					}
				}
			})

			t.Run("limit is honoured", func(t *testing.T) {
				s := f.make(t)
				seed(t, s, "r1", 50)

				got, _ := s.Get(context.Background(), "r1", 0, 10)
				if len(got) != 10 {
					t.Fatalf("Get(limit=10) = %d events, want 10", len(got))
				}
			})

			t.Run("runs are isolated", func(t *testing.T) {
				s := f.make(t)
				seed(t, s, "r1", 5)
				seed(t, s, "r2", 3)

				got, _ := s.Get(context.Background(), "r1", 0, 100)
				if len(got) != 5 {
					t.Fatalf("Get(r1) = %d events, want 5; runs leaked into each other", len(got))
				}
				for _, e := range got {
					if e.RunID != "r1" {
						t.Fatalf("Get(r1) returned an event from %q", e.RunID)
					}
				}
			})

			t.Run("unknown run is empty not an error", func(t *testing.T) {
				s := f.make(t)

				got, err := s.Get(context.Background(), "nope", 0, 100)
				if err != nil {
					t.Fatalf("Get() on an unknown run error = %v, want nil", err)
				}
				if len(got) != 0 {
					t.Fatalf("Get() = %d events, want 0", len(got))
				}
			})

			t.Run("cursor past the end is empty", func(t *testing.T) {
				s := f.make(t)
				seed(t, s, "r1", 5)

				got, _ := s.Get(context.Background(), "r1", 999, 100)
				if len(got) != 0 {
					t.Fatalf("Get(afterSeq=999) = %d events, want 0", len(got))
				}
			})

			t.Run("empty batch is a no-op", func(t *testing.T) {
				s := f.make(t)
				if err := s.PutBatch(context.Background(), nil); err != nil {
					t.Fatalf("PutBatch(nil) error = %v", err)
				}
			})
		})
	}
}

func TestMemoryEventStoreDeduplicatesIdempotencyKeyAndSequence(t *testing.T) {
	s := runtime.NewMemoryEventStore()
	first := runtime.MustEvent("run-1", "thread-1", runtime.EventToolStart, nil)
	first.Seq = 1
	first.IdempotencyKey = "tool:call-1:start"
	duplicateKey := first
	duplicateKey.Data = []byte(`{"changed":true}`)
	duplicateSeq := runtime.MustEvent("run-1", "thread-1", runtime.EventToolResult, nil)
	duplicateSeq.Seq = 1
	if err := s.PutBatch(context.Background(), []runtime.Event{first, duplicateKey, duplicateSeq}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(context.Background(), "run-1", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].IdempotencyKey != first.IdempotencyKey {
		t.Fatalf("stored events = %+v", got)
	}
}

func TestMemoryEventStoreReportsSequenceHighWaterMark(t *testing.T) {
	store := runtime.NewMemoryEventStore()
	seed(t, store, "run-1", 7)
	last, err := store.LastSeq(context.Background(), "run-1")
	if err != nil || last != 7 {
		t.Fatalf("LastSeq() = %d, %v; want 7", last, err)
	}
}

// --- Replay 分页 ---

// token 级流式一轮就能产生上千条 delta。单次取一页或设总量上限会静默截断，
// 客户端既拿不到回答尾部也拿不到 run_end。
func TestReplay_PaginatesToExhaustion(t *testing.T) {
	t.Parallel()

	s := runtime.NewMemoryEventStore()
	const total = 1_500
	seed(t, s, "r1", total)

	var got []int64
	err := runtime.Replay(context.Background(), s, "r1", 0, 200, func(e runtime.Event) bool {
		got = append(got, e.Seq)
		return true
	})
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if len(got) != total {
		t.Fatalf("Replay() yielded %d events, want %d; the tail was silently truncated", len(got), total)
	}
	for i, seq := range got {
		if seq != int64(i+1) {
			t.Fatalf("Replay() yielded seq %d at position %d", seq, i)
		}
	}
}

func TestReplay_StartsAfterCursor(t *testing.T) {
	t.Parallel()

	s := runtime.NewMemoryEventStore()
	seed(t, s, "r1", 500)

	var first int64
	var count int
	err := runtime.Replay(context.Background(), s, "r1", 400, 50, func(e runtime.Event) bool {
		if count == 0 {
			first = e.Seq
		}
		count++
		return true
	})
	if err != nil {
		t.Fatal(err)
	}

	if first != 401 {
		t.Fatalf("first replayed seq = %d, want 401", first)
	}
	if count != 100 {
		t.Fatalf("replayed %d events, want 100", count)
	}
}

func TestReplay_StopsWhenYieldReturnsFalse(t *testing.T) {
	t.Parallel()

	s := runtime.NewMemoryEventStore()
	seed(t, s, "r1", 500)

	var count int
	err := runtime.Replay(context.Background(), s, "r1", 0, 50, func(runtime.Event) bool {
		count++
		return count < 10 // 客户端断开
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 10 {
		t.Fatalf("yielded %d events after the consumer stopped, want 10", count)
	}
}

func TestReplay_RespectsCancellation(t *testing.T) {
	t.Parallel()

	s := runtime.NewMemoryEventStore()
	seed(t, s, "r1", 100)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runtime.Replay(ctx, s, "r1", 0, 10, func(runtime.Event) bool { return true })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Replay() error = %v, want context.Canceled", err)
	}
}

func TestReplay_EmptyRun(t *testing.T) {
	t.Parallel()

	s := runtime.NewMemoryEventStore()
	var count int
	err := runtime.Replay(context.Background(), s, "empty", 0, 10, func(runtime.Event) bool {
		count++
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("yielded %d events for an empty run", count)
	}
}

func TestReplay_PropagatesStoreErrors(t *testing.T) {
	t.Parallel()

	err := runtime.Replay(context.Background(), failingStore{}, "r1", 0, 10,
		func(runtime.Event) bool { return true })

	if err == nil {
		t.Fatal("Replay() swallowed a store error")
	}
}

// --- AsyncPersister ---

func TestAsyncPersister_FlushesToStore(t *testing.T) {
	t.Parallel()

	s := runtime.NewMemoryEventStore()
	p := runtime.NewAsyncPersister(s, runtime.PersisterOptions{
		BatchSize:     10,
		FlushInterval: 10 * time.Millisecond,
	})

	for i := range 25 {
		e := runtime.MustEvent("r1", "t1", runtime.EventContentDelta, map[string]int{"i": i})
		e.Seq = int64(i + 1)
		p.Persist(context.Background(), e)
	}
	p.Close()

	got, err := s.Get(context.Background(), "r1", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 25 {
		t.Fatalf("store has %d events, want 25", len(got))
	}
}

// Persist 由 Publish 同步调用，必须立刻返回，否则等于阻塞主循环。
func TestAsyncPersister_PersistDoesNotBlockOnSlowStore(t *testing.T) {
	t.Parallel()

	p := runtime.NewAsyncPersister(slowStore{delay: 50 * time.Millisecond},
		runtime.PersisterOptions{QueueSize: 1024, BatchSize: 1, CloseTimeout: 50 * time.Millisecond})
	defer p.Close()

	start := time.Now()
	for range 100 {
		p.Persist(context.Background(), runtime.MustEvent("r1", "t1", runtime.EventContentDelta, nil))
	}

	// 100 条事件如果同步等存储，至少要 5 秒；非阻塞实现应当毫秒级返回。
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Persist took %v for 100 trace events; it must not wait on the store", elapsed)
	}
}

// 队列满时 trace 类直接丢弃：观测数据的完整性不值得拖慢回合。
func TestAsyncPersister_DropsTraceEventsWhenSaturated(t *testing.T) {
	t.Parallel()

	p := runtime.NewAsyncPersister(blockingStore{}, runtime.PersisterOptions{
		QueueSize:    2,
		BatchSize:    1,
		CloseTimeout: 50 * time.Millisecond,
	})
	defer p.Close()

	start := time.Now()
	for range 200 {
		p.Persist(context.Background(), runtime.MustEvent("r1", "t1", runtime.EventContentDelta, nil))
	}

	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Persist took %v; saturated trace events must be dropped, not awaited", elapsed)
	}
	if p.Dropped() == 0 {
		t.Fatal("Dropped() = 0; drops must be counted so they are observable")
	}
}

// audit 类事件在队列满时有限等待，超时记 error 而不是静默丢弃 ——
// 一条被静默丢掉的 audit 事件意味着审计记录有洞，而没人会知道。
func TestAsyncPersister_AuditEventsWaitBeforeDropping(t *testing.T) {
	t.Parallel()

	// 用闸门精确控制饱和时机：靠 sleep 制造竞态会让这个测试时而通过时而失败，
	// 而 flaky 测试最终的下场是被人加 t.Skip。
	store := newGatedStore()
	defer store.release()

	p := runtime.NewAsyncPersister(store, runtime.PersisterOptions{
		QueueSize:         1,
		BatchSize:         1,
		AuditBlockTimeout: 100 * time.Millisecond,
		CloseTimeout:      50 * time.Millisecond,
	})
	defer p.Close()

	ctx := context.Background()

	// 第一条被后台 goroutine 取走并卡在 PutBatch 上
	p.Persist(ctx, runtime.MustEvent("r1", "t1", runtime.EventContentDelta, nil))
	store.waitUntilBlocked(t)

	// 第二条填满队列（容量 1）
	p.Persist(ctx, runtime.MustEvent("r1", "t1", runtime.EventContentDelta, nil))

	audit := runtime.MustEvent("r1", "t1", runtime.EventMessageReplace, nil)
	if audit.Droppable() {
		t.Fatal("message_replace must not be droppable")
	}

	start := time.Now()
	p.Persist(ctx, audit)
	elapsed := time.Since(start)

	// 必须等过 AuditBlockTimeout，不能像 trace 那样立刻丢
	if elapsed < 50*time.Millisecond {
		t.Fatalf("audit event was dropped after %v; it must wait for the configured timeout", elapsed)
	}
	if elapsed > time.Second {
		t.Fatalf("audit event waited %v; the timeout must bound the wait", elapsed)
	}
}

func TestAsyncPersister_CloseIsIdempotent(t *testing.T) {
	t.Parallel()

	p := runtime.NewAsyncPersister(runtime.NewMemoryEventStore(), runtime.PersisterOptions{})
	p.Close()
	p.Close()
}

// 存储卡住时关机不该无限期挂着：关不掉的进程比丢几条 trace 事件糟得多。
func TestAsyncPersister_CloseIsBounded(t *testing.T) {
	t.Parallel()

	p := runtime.NewAsyncPersister(blockingStore{}, runtime.PersisterOptions{
		BatchSize:    1,
		CloseTimeout: 20 * time.Millisecond,
	})
	p.Persist(context.Background(), runtime.MustEvent("r1", "t1", runtime.EventContentDelta, nil))

	start := time.Now()
	p.Close()

	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("Close() took %v; it must give up on a stuck store", elapsed)
	}
}

// 与 Bus 串起来：事件必须带着 Bus 赋予的 Seq 落到存储里。
func TestAsyncPersister_IntegratesWithBus(t *testing.T) {
	t.Parallel()

	s := runtime.NewMemoryEventStore()
	p := runtime.NewAsyncPersister(s, runtime.PersisterOptions{
		BatchSize:     5,
		FlushInterval: 10 * time.Millisecond,
	})

	b := runtime.NewMemoryBus(runtime.BusOptions{Persister: p})
	ctx := context.Background()
	for range 12 {
		b.Publish(ctx, runtime.MustEvent("r1", "t1", runtime.EventContentDelta, nil))
	}
	p.Close()

	got, err := s.Get(ctx, "r1", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 12 {
		t.Fatalf("store has %d events, want 12", len(got))
	}
	for i, e := range got {
		if e.Seq != int64(i+1) {
			t.Fatalf("stored seq %d at position %d; the bus sequence must be preserved", e.Seq, i)
		}
	}
}

// --- helpers ---

type failingStore struct{}

func (failingStore) PutBatch(context.Context, []runtime.Event) error {
	return errors.New("storage down")
}

func (failingStore) Get(context.Context, string, int64, int) ([]runtime.Event, error) {
	return nil, errors.New("storage down")
}

type slowStore struct{ delay time.Duration }

func (s slowStore) PutBatch(context.Context, []runtime.Event) error {
	time.Sleep(s.delay)
	return nil
}

func (slowStore) Get(context.Context, string, int64, int) ([]runtime.Event, error) {
	return nil, nil
}

// blockingStore 迟迟不返回，用于制造队列饱和。
//
// 时长只需远大于测试里的各项超时即可 —— 用秒级会让整个包的测试拖到
// 几十秒，而慢测试最终的下场是被人跳过。
type blockingStore struct{}

func (blockingStore) PutBatch(context.Context, []runtime.Event) error {
	time.Sleep(300 * time.Millisecond)
	return nil
}

func (blockingStore) Get(context.Context, string, int64, int) ([]runtime.Event, error) {
	return nil, nil
}

// gatedStore 的 PutBatch 挂在闸门上，直到测试放行。
//
// 它让"队列已满"成为确定事实而不是靠 sleep 赌出来的时序。
type gatedStore struct {
	entered chan struct{}
	gate    chan struct{}
	once    sync.Once
}

func newGatedStore() *gatedStore {
	return &gatedStore{
		entered: make(chan struct{}, 1),
		gate:    make(chan struct{}),
	}
}

func (g *gatedStore) PutBatch(context.Context, []runtime.Event) error {
	select {
	case g.entered <- struct{}{}:
	default:
	}
	<-g.gate
	return nil
}

func (g *gatedStore) Get(context.Context, string, int64, int) ([]runtime.Event, error) {
	return nil, nil
}

// waitUntilBlocked 等到后台 goroutine 真正进入 PutBatch。
func (g *gatedStore) waitUntilBlocked(t *testing.T) {
	t.Helper()

	select {
	case <-g.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the persister never reached the store")
	}
}

func (g *gatedStore) release() {
	g.once.Do(func() { close(g.gate) })
}

var _ = fmt.Sprintf
