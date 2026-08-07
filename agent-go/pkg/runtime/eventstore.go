package runtime

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// EventStore 是可插拔的事件存储。
//
// 内存与 SQL 两实现必须遵守同一份游标语义。忽略 afterSeq 的测试替身会让
// 分页与重连测试全绿，而真实路径永远返回同一页 —— 这是设计文档 §5 明列的坑，
// 所以两个实现共用一份契约测试。
type EventStore interface {
	// PutBatch 批量写入事件。
	PutBatch(ctx context.Context, events []Event) error

	// Get 返回某个 run 内 Seq > afterSeq 的事件，按 Seq 升序，最多 limit 条。
	//
	// 必须真的从 afterSeq 之后取。回放要分页到取空，一个忽略游标的实现
	// 会让回放变成无限循环。
	Get(ctx context.Context, runID string, afterSeq int64, limit int) ([]Event, error)
}

// MemoryEventStore 是内存实现，供测试与无持久化部署使用。
type MemoryEventStore struct {
	mu    sync.RWMutex
	byRun map[string][]Event
}

// NewMemoryEventStore 返回内存事件存储。
func NewMemoryEventStore() *MemoryEventStore {
	return &MemoryEventStore{byRun: make(map[string][]Event)}
}

// PutBatch 实现 EventStore。
func (s *MemoryEventStore) PutBatch(_ context.Context, events []Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, e := range events {
		s.byRun[e.RunID] = append(s.byRun[e.RunID], e)
	}
	return nil
}

// Get 实现 EventStore。
func (s *MemoryEventStore) Get(_ context.Context, runID string, afterSeq int64, limit int) ([]Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []Event
	for _, e := range s.byRun[runID] {
		if e.Seq <= afterSeq {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// AsyncPersister 把事件异步批量写入 EventStore。
//
// 它是 Bus 的 Persister：Publish 会同步调用 Persist，所以这里必须立刻返回。
// 写入在后台 goroutine 里批量 flush（设计文档 §12.5）。
type AsyncPersister struct {
	store EventStore
	opts  PersisterOptions

	queue   chan Event
	done    chan struct{}
	once    sync.Once
	queueMu sync.RWMutex
	closed  bool

	mu      sync.Mutex
	dropped int64
}

// PersisterOptions 配置异步持久化。
type PersisterOptions struct {
	// QueueSize 是待写队列容量。<= 0 时用 4096。
	QueueSize int

	// BatchSize 是单次 flush 的最大条数。<= 0 时用 128。
	BatchSize int

	// FlushInterval 是强制 flush 的间隔。<= 0 时用 200ms。
	FlushInterval time.Duration

	// AuditBlockTimeout 是队列满时为不可丢弃事件等待的上限。
	//
	// <= 0 时用 2 秒。超时后记 error 而不是静默丢弃 —— 一条被静默丢掉的
	// audit 事件意味着审计记录有洞，而没人会知道。
	AuditBlockTimeout time.Duration

	// CloseTimeout 是 Close 等待队列排空的上限。<= 0 时用 5 秒。
	CloseTimeout time.Duration

	Logger *slog.Logger
}

func (o PersisterOptions) withDefaults() PersisterOptions {
	if o.QueueSize <= 0 {
		o.QueueSize = 4096
	}
	if o.BatchSize <= 0 {
		o.BatchSize = 128
	}
	if o.FlushInterval <= 0 {
		o.FlushInterval = 200 * time.Millisecond
	}
	if o.AuditBlockTimeout <= 0 {
		o.AuditBlockTimeout = 2 * time.Second
	}
	if o.CloseTimeout <= 0 {
		o.CloseTimeout = 5 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return o
}

// NewAsyncPersister 返回异步持久化器并启动后台 flush。
func NewAsyncPersister(store EventStore, opts PersisterOptions) *AsyncPersister {
	opts = opts.withDefaults()

	p := &AsyncPersister{
		store: store,
		opts:  opts,
		queue: make(chan Event, opts.QueueSize),
		done:  make(chan struct{}),
	}
	go p.run()
	return p
}

// Persist 实现 Bus 的 Persister。
//
// trace 类事件在队列满时直接丢弃并计数；audit 类事件最多等
// AuditBlockTimeout，超时记 error。
func (p *AsyncPersister) Persist(ctx context.Context, e Event) {
	p.queueMu.RLock()
	defer p.queueMu.RUnlock()
	if p.closed {
		return
	}
	select {
	case p.queue <- e:
		return
	default:
	}

	if e.Droppable() {
		p.countDrop()
		return
	}

	// 不可丢弃事件：有限等待。这里是唯一允许短暂阻塞的地方，
	// 因为丢掉审计记录的代价高于一次短暂延迟。
	timer := time.NewTimer(p.opts.AuditBlockTimeout)
	defer timer.Stop()

	select {
	case p.queue <- e:
	case <-timer.C:
		p.countDrop()
		p.opts.Logger.ErrorContext(ctx, "dropped a non-droppable event; the audit trail has a gap",
			"run_id", e.RunID, "seq", e.Seq, "type", e.Type, "category", e.Category)
	case <-ctx.Done():
		p.countDrop()
	}
}

func (p *AsyncPersister) countDrop() {
	p.mu.Lock()
	p.dropped++
	p.mu.Unlock()
}

// Dropped 返回被丢弃的事件数，供可观测性使用。
func (p *AsyncPersister) Dropped() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dropped
}

func (p *AsyncPersister) run() {
	defer close(p.done)

	ticker := time.NewTicker(p.opts.FlushInterval)
	defer ticker.Stop()

	batch := make([]Event, 0, p.opts.BatchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := p.store.PutBatch(context.Background(), batch); err != nil {
			p.opts.Logger.Error("persisting events failed", "count", len(batch), "error", err)
		}
		batch = batch[:0]
	}

	for {
		select {
		case e, ok := <-p.queue:
			if !ok {
				flush()
				return
			}
			batch = append(batch, e)
			if len(batch) >= p.opts.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// Close 停止后台 flush 并写出剩余事件。
//
// 等待有上限：存储卡住时，一次关机不该无限期挂在那里。超时后放弃剩余批次
// 并记 error —— 关不掉的进程比丢几条 trace 事件糟得多。
func (p *AsyncPersister) Close() {
	p.once.Do(func() {
		p.queueMu.Lock()
		p.closed = true
		close(p.queue)
		p.queueMu.Unlock()

		timer := time.NewTimer(p.opts.CloseTimeout)
		defer timer.Stop()

		select {
		case <-p.done:
		case <-timer.C:
			p.opts.Logger.Error("event persister did not drain before shutdown",
				"timeout", p.opts.CloseTimeout)
		}
	})
}

// Replay 从 store 分页读取事件，直到取空或 yield 返回 false。
//
// 三条约束里的"分页到取空"就在这里（设计文档 §12.5）：token 级流式下
// 一轮问答就能产生上千条 content_delta，单次取 200 条或设总量上限都会
// 静默截断，客户端既拿不到回答尾部也拿不到 run_end。
func Replay(
	ctx context.Context, store EventStore, runID string, afterSeq int64, pageSize int,
	yield func(Event) bool,
) error {
	if pageSize <= 0 {
		pageSize = 200
	}
	cursor := afterSeq

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		page, err := store.Get(ctx, runID, cursor, pageSize)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}

		for _, e := range page {
			if !yield(e) {
				return nil
			}
			cursor = e.Seq
		}

		// 不足一页说明已经取空。继续请求只会白跑一次查询。
		if len(page) < pageSize {
			return nil
		}
	}
}
