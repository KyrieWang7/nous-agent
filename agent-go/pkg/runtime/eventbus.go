package runtime

import (
	"context"
	"log/slog"
	"sync"
)

// 默认容量。
const (
	defaultSubscriberBuffer = 256
	defaultRingCapacity     = 500
)

// Bus 是事件总线。
//
// 三通路（设计文档 §12.2）：
//   - 实时：每订阅者一个有界 channel，允许丢失
//   - 缓冲：每 run 一个环形缓冲，供秒级断线重连回放
//   - 持久：Redis Stream，跨实例订阅与精确恢复的权威通路
//
// MemoryBus 实现前两路。第三路由 Persister 注入，缺失时降级为仅内存。
type Bus interface {
	// Publish 发布事件并返回赋予的 Seq。
	//
	// 必须非阻塞：事件通路的任何拥塞都不得卡住 agent 回合。
	Publish(ctx context.Context, e Event) int64

	// Subscribe 返回事件通道与退订函数。
	Subscribe(runID string) (<-chan Event, func())

	// Backlog 返回缓冲里 Seq > afterSeq 的事件，用于秒级重连回放。
	Backlog(runID string, afterSeq int64) []Event

	// SubscriberCount 返回某个 run 的订阅者数量。
	//
	// on_disconnect=cancel 的判定要用它：只有最后一个订阅者也走了
	// 才取消 run（设计文档 §12.3）。
	SubscriberCount(runID string) int

	// Close 释放某个 run 的资源。
	Close(runID string)
}

// Persister 把事件写入持久通路。
//
// 实现必须自行异步化：Publish 会同步调用它，阻塞就等于阻塞主循环。
type Persister interface {
	Persist(ctx context.Context, e Event)
}

// MemoryBus 是内存两通路实现。
type MemoryBus struct {
	opts BusOptions

	mu   sync.Mutex
	runs map[string]*runChannel
}

// BusOptions 配置事件总线。
type BusOptions struct {
	// SubscriberBuffer 是每个订阅者通道的容量。<= 0 时用 256。
	SubscriberBuffer int

	// RingCapacity 是每个 run 的环形缓冲容量。<= 0 时用 500。
	RingCapacity int

	// Persister 是持久通路。为 nil 时只走内存。
	Persister Persister

	Logger *slog.Logger
}

func (o BusOptions) withDefaults() BusOptions {
	if o.SubscriberBuffer <= 0 {
		o.SubscriberBuffer = defaultSubscriberBuffer
	}
	if o.RingCapacity <= 0 {
		o.RingCapacity = defaultRingCapacity
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return o
}

// NewMemoryBus 返回内存事件总线。
func NewMemoryBus(opts BusOptions) *MemoryBus {
	return &MemoryBus{
		opts: opts.withDefaults(),
		runs: make(map[string]*runChannel),
	}
}

// runChannel 是单个 run 的通路状态。
type runChannel struct {
	mu   sync.Mutex
	seq  int64
	ring *ring
	subs map[int]*subscriber
	next int
}

type subscriber struct {
	ch      chan Event
	dropped int64
}

func (b *MemoryBus) channelFor(runID string) *runChannel {
	b.mu.Lock()
	defer b.mu.Unlock()

	rc, ok := b.runs[runID]
	if !ok {
		rc = &runChannel{
			ring: newRing(b.opts.RingCapacity),
			subs: make(map[int]*subscriber),
		}
		b.runs[runID] = rc
	}
	return rc
}

// Publish 实现 Bus。
func (b *MemoryBus) Publish(ctx context.Context, e Event) int64 {
	rc := b.channelFor(e.RunID)

	rc.mu.Lock()
	if e.Seq > rc.seq {
		rc.seq = e.Seq
	} else {
		rc.seq++
		e.Seq = rc.seq
	}
	if e.Category == "" {
		e.Category = categoryOf(e.Type)
	}
	rc.ring.push(e)

	// 在锁内投递：Seq 的赋值与投递必须原子，否则并发发布会让订阅者
	// 收到乱序的 Seq，而 Last-Event-ID 游标依赖单调性。
	for _, s := range rc.subs {
		select {
		case s.ch <- e:
		default:
			// 通道满时丢弃并计数，绝不阻塞。
			//
			// 丢弃是安全的，因为 Seq 单调：客户端能从 ID 不连续发现缺口，
			// 再用 Backlog 或持久通路补齐。这正是环形缓冲存在的理由。
			s.dropped++
		}
	}
	rc.mu.Unlock()

	if b.opts.Persister != nil {
		b.opts.Persister.Persist(ctx, e)
	}
	return e.Seq
}

// Subscribe 实现 Bus。
func (b *MemoryBus) Subscribe(runID string) (<-chan Event, func()) {
	rc := b.channelFor(runID)

	rc.mu.Lock()
	id := rc.next
	rc.next++
	s := &subscriber{ch: make(chan Event, b.opts.SubscriberBuffer)}
	rc.subs[id] = s
	rc.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			rc.mu.Lock()
			delete(rc.subs, id)
			dropped := s.dropped
			rc.mu.Unlock()

			// 关闭通道让 SSE 侧的 range 自然结束。退订与关闭都在锁外，
			// 且 Publish 只在锁内向存活的订阅者投递，所以不会向已关闭的
			// 通道写入。
			close(s.ch)

			if dropped > 0 {
				b.opts.Logger.Warn("subscriber dropped events",
					"run_id", runID, "dropped", dropped)
			}
		})
	}
	return s.ch, cancel
}

// Backlog 实现 Bus。
func (b *MemoryBus) Backlog(runID string, afterSeq int64) []Event {
	rc := b.channelFor(runID)

	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.ring.since(afterSeq)
}

// SubscriberCount 实现 Bus。
func (b *MemoryBus) SubscriberCount(runID string) int {
	rc := b.channelFor(runID)

	rc.mu.Lock()
	defer rc.mu.Unlock()
	return len(rc.subs)
}

// Close 实现 Bus。
func (b *MemoryBus) Close(runID string) {
	b.mu.Lock()
	rc, ok := b.runs[runID]
	delete(b.runs, runID)
	b.mu.Unlock()

	if !ok {
		return
	}

	rc.mu.Lock()
	subs := rc.subs
	rc.subs = make(map[int]*subscriber)
	rc.mu.Unlock()

	for _, s := range subs {
		close(s.ch)
	}
}

// ring 是每个 run 的定长环形缓冲。
type ring struct {
	buf   []Event
	start int
	size  int
}

func newRing(capacity int) *ring {
	return &ring{buf: make([]Event, capacity)}
}

func (r *ring) push(e Event) {
	if len(r.buf) == 0 {
		return
	}
	if r.size < len(r.buf) {
		r.buf[(r.start+r.size)%len(r.buf)] = e
		r.size++
		return
	}
	// 满了：覆盖最旧的一条并前移起点。
	r.buf[r.start] = e
	r.start = (r.start + 1) % len(r.buf)
}

// since 返回 Seq > afterSeq 的事件，按 Seq 升序。
func (r *ring) since(afterSeq int64) []Event {
	var out []Event
	for i := range r.size {
		e := r.buf[(r.start+i)%len(r.buf)]
		if e.Seq > afterSeq {
			out = append(out, e)
		}
	}
	return out
}

// oldestSeq 返回缓冲里最旧事件的 Seq，空缓冲返回 0。
//
// SSE 侧用它判断"请求的游标是否已经被覆盖掉了"：若 afterSeq + 1 < oldestSeq，
// 说明中间有一段永久丢失，必须提示客户端做全量刷新而不是假装连续。
func (r *ring) oldestSeq() int64 {
	if r.size == 0 {
		return 0
	}
	return r.buf[r.start].Seq
}

// BacklogGap 报告从 afterSeq 恢复是否会漏掉事件。
//
// 返回 true 时客户端必须重新拉取完整状态：假装连续会让它渲染出
// 缺了一段的回答，而用户看不出哪里缺了。
func (b *MemoryBus) BacklogGap(runID string, afterSeq int64) bool {
	rc := b.channelFor(runID)

	rc.mu.Lock()
	defer rc.mu.Unlock()

	oldest := rc.ring.oldestSeq()
	if oldest == 0 {
		return false
	}
	return afterSeq+1 < oldest
}
