package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrRunNotFound 表示 run 不存在或已过期。
var ErrRunNotFound = errors.New("runtime: run not found")

// DisconnectPolicy 决定客户端断开连接后 run 怎么办。
type DisconnectPolicy string

const (
	// DisconnectCancel 断连即取消。适用于即时问答：没人在读的回答继续
	// 生成就是纯烧钱。
	DisconnectCancel DisconnectPolicy = "cancel"

	// DisconnectContinue 断连后继续跑，结果落库。适用于长任务：
	// 连接可断、任务不断（设计文档 §12.3）。
	DisconnectContinue DisconnectPolicy = "continue"
)

// Valid 报告策略是否已知。
func (p DisconnectPolicy) Valid() bool {
	return p == DisconnectCancel || p == DisconnectContinue
}

// RunStatus 是 run 的生命周期状态。
type RunStatus string

const (
	StatusRunning   RunStatus = "running"
	StatusCompleted RunStatus = "completed"
	StatusCancelled RunStatus = "cancelled"
	StatusFailed    RunStatus = "failed"
)

// Terminal 报告该状态是否已终结。
func (s RunStatus) Terminal() bool {
	return s == StatusCompleted || s == StatusCancelled || s == StatusFailed
}

// RunRecord 是 run 的元数据，跨实例可见。
type RunRecord struct {
	RunID        string           `json:"run_id"`
	ThreadID     string           `json:"thread_id"`
	AssistantID  string           `json:"assistant_id,omitempty"`
	Status       RunStatus        `json:"status"`
	OnDisconnect DisconnectPolicy `json:"on_disconnect"`
	ModelName    string           `json:"model_name,omitempty"`
	StartedAt    time.Time        `json:"started_at"`
	HeartbeatAt  time.Time        `json:"heartbeat_at"`
	CompletedAt  *time.Time       `json:"completed_at,omitempty"`

	// Owner 是持有该 run 的实例标识，用于诊断"取消命令发给了谁"。
	Owner string `json:"owner,omitempty"`
}

// RegistryBackend 是 run 注册表的存储与广播后端。
//
// 生产实现以 Redis 为权威（Hash 存元数据 + Pub/Sub 广播取消）。
// nous-agent 的 src/core/task_registry.py 是进程内 dict，多实例下取消会失效、
// 状态会不一致，本设计不采纳（设计文档 §12.3）。
type RegistryBackend interface {
	// Save 写入或更新记录，并刷新 TTL。
	Save(ctx context.Context, rec RunRecord, ttl time.Duration) error

	// Load 读取记录。不存在时返回 ErrRunNotFound。
	Load(ctx context.Context, runID string) (RunRecord, error)

	// Delete 删除记录。
	Delete(ctx context.Context, runID string) error

	// BroadcastCancel 向所有实例广播取消信号。
	BroadcastCancel(ctx context.Context, runID string) error

	// SubscribeCancel 返回取消信号通道与退订函数。
	SubscribeCancel(ctx context.Context) (<-chan string, func(), error)
}

// Registry 管理 run 的生命周期与取消。
//
// 元数据以后端为权威，进程内只缓存本实例持有的 context.CancelFunc ——
// 取消函数是不可序列化的，只能留在本地；而"谁该被取消"必须全局可见。
type Registry struct {
	backend RegistryBackend
	owner   string
	ttl     time.Duration

	mu    sync.Mutex
	local map[string]context.CancelFunc

	stopWatch func()
}

// RegistryOptions 配置注册表。
type RegistryOptions struct {
	// Backend 是权威存储。为 nil 时用进程内实现（仅适用于单实例部署）。
	Backend RegistryBackend

	// Owner 标识本实例。
	Owner string

	// TTL 是记录的存活时长。<= 0 时用 10 分钟。
	//
	// 长跑 run 必须定期 Touch 续期：TTL 过期后取消命令找不到目标，
	// 一个跑了两小时的视频分析就再也停不下来。
	TTL time.Duration
}

// NewRegistry 返回注册表并开始监听取消广播。
func NewRegistry(ctx context.Context, opts RegistryOptions) (*Registry, error) {
	backend := opts.Backend
	if backend == nil {
		backend = NewMemoryRegistryBackend()
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}

	r := &Registry{
		backend: backend,
		owner:   opts.Owner,
		ttl:     ttl,
		local:   make(map[string]context.CancelFunc),
	}

	signals, stop, err := backend.SubscribeCancel(ctx)
	if err != nil {
		return nil, fmt.Errorf("runtime: subscribing to cancel signals: %w", err)
	}
	r.stopWatch = stop

	go r.watch(signals)
	return r, nil
}

// watch 消费取消广播并触发本实例持有的 CancelFunc。
func (r *Registry) watch(signals <-chan string) {
	for runID := range signals {
		r.mu.Lock()
		cancel, ok := r.local[runID]
		r.mu.Unlock()

		if ok {
			cancel()
		}
	}
}

// Register 登记一个新 run，返回可取消的 context。
func (r *Registry) Register(ctx context.Context, rec RunRecord) (context.Context, error) {
	if rec.RunID == "" {
		return nil, errors.New("runtime: run id must not be empty")
	}
	if !rec.OnDisconnect.Valid() {
		rec.OnDisconnect = DisconnectCancel
	}
	if rec.Status == "" {
		rec.Status = StatusRunning
	}
	if rec.StartedAt.IsZero() {
		rec.StartedAt = time.Now().UTC()
	}
	rec.HeartbeatAt = rec.StartedAt
	rec.Owner = r.owner

	if err := r.backend.Save(ctx, rec, r.ttl); err != nil {
		return nil, fmt.Errorf("runtime: registering run: %w", err)
	}

	// context.WithoutCancel 让 run 的生命周期脱离发起它的 HTTP 请求：
	// on_disconnect=continue 的语义就靠这个成立。取消只能来自
	// Cancel() 或显式的超时。
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	r.mu.Lock()
	r.local[rec.RunID] = cancel
	r.mu.Unlock()

	return runCtx, nil
}

// Get 读取 run 记录。
func (r *Registry) Get(ctx context.Context, runID string) (RunRecord, error) {
	return r.backend.Load(ctx, runID)
}

// Cancel 取消一个 run。跨实例生效。
func (r *Registry) Cancel(ctx context.Context, runID string) error {
	rec, err := r.backend.Load(ctx, runID)
	if err != nil {
		return err
	}
	if rec.Status.Terminal() {
		return nil // 已经结束了，取消是空操作
	}

	// 先本地取消再广播：本实例持有时立刻生效，不必等一圈 Pub/Sub。
	r.mu.Lock()
	cancel, local := r.local[runID]
	r.mu.Unlock()
	if local {
		cancel()
	}

	if err := r.backend.BroadcastCancel(ctx, runID); err != nil {
		return fmt.Errorf("runtime: broadcasting cancel: %w", err)
	}
	return nil
}

// Complete 标记 run 结束并清理本地状态。
func (r *Registry) Complete(ctx context.Context, runID string, status RunStatus) error {
	rec, err := r.backend.Load(ctx, runID)
	if err != nil {
		// 记录已过期不该让回合失败：run 确实跑完了，只是元数据没了。
		if errors.Is(err, ErrRunNotFound) {
			r.forget(runID)
			return nil
		}
		return err
	}

	now := time.Now().UTC()
	rec.Status = status
	rec.CompletedAt = &now

	if err := r.backend.Save(ctx, rec, r.ttl); err != nil {
		return fmt.Errorf("runtime: completing run: %w", err)
	}
	r.forget(runID)
	return nil
}

func (r *Registry) forget(runID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.local, runID)
}

// Touch 刷新 TTL。长跑 run 必须定期调用。
func (r *Registry) Touch(ctx context.Context, runID string) error {
	rec, err := r.backend.Load(ctx, runID)
	if err != nil {
		return err
	}
	rec.HeartbeatAt = time.Now().UTC()
	return r.backend.Save(ctx, rec, r.ttl)
}

// KeepAlive 每 interval 刷新一次 TTL，直到 ctx 结束。
func (r *Registry) KeepAlive(ctx context.Context, runID string, interval time.Duration) {
	if interval <= 0 {
		interval = r.ttl / 2
	}
	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// 刷新失败只记不中断：一次网络抖动不该杀掉一个正在跑的长任务。
			_ = r.Touch(ctx, runID)
		}
	}
}

// LocalCount 返回本实例持有的 run 数量。
func (r *Registry) LocalCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.local)
}

// ClientDisconnected 在客户端断开时按策略处理。
//
// remaining 是该 run 剩余的订阅者数量。只有降到 0 才考虑取消：
// 多端同时观察时，关掉一个窗口不该杀掉别人正在看的 run。
func (r *Registry) ClientDisconnected(ctx context.Context, runID string, remaining int) error {
	if remaining > 0 {
		return nil
	}

	rec, err := r.backend.Load(ctx, runID)
	if err != nil {
		if errors.Is(err, ErrRunNotFound) {
			return nil
		}
		return err
	}
	if rec.OnDisconnect != DisconnectCancel || rec.Status.Terminal() {
		return nil
	}
	return r.Cancel(ctx, runID)
}

// Close 停止监听取消广播。
func (r *Registry) Close() {
	if r.stopWatch != nil {
		r.stopWatch()
	}
}

// MemoryRegistryBackend 是进程内后端。
//
// 只适用于单实例部署与测试。多实例部署必须换 Redis 后端，
// 否则发给实例 A 的取消命令永远到不了持有该 run 的实例 B。
type MemoryRegistryBackend struct {
	mu      sync.Mutex
	records map[string]memoryRecord
	subs    map[int]chan string
	next    int
}

type memoryRecord struct {
	rec       RunRecord
	expiresAt time.Time
}

// NewMemoryRegistryBackend 返回进程内后端。
func NewMemoryRegistryBackend() *MemoryRegistryBackend {
	return &MemoryRegistryBackend{
		records: make(map[string]memoryRecord),
		subs:    make(map[int]chan string),
	}
}

// Save 实现 RegistryBackend。
func (m *MemoryRegistryBackend) Save(_ context.Context, rec RunRecord, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[rec.RunID] = memoryRecord{rec: rec, expiresAt: time.Now().Add(ttl)}
	return nil
}

// Load 实现 RegistryBackend。
func (m *MemoryRegistryBackend) Load(_ context.Context, runID string) (RunRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.records[runID]
	if !ok {
		return RunRecord{}, fmt.Errorf("%w: %q", ErrRunNotFound, runID)
	}
	// TTL 语义必须与 Redis 实现一致，否则"过期后取消找不到目标"这个
	// 失效模式在测试里永远复现不出来。
	if time.Now().After(entry.expiresAt) {
		delete(m.records, runID)
		return RunRecord{}, fmt.Errorf("%w: %q expired", ErrRunNotFound, runID)
	}
	return entry.rec, nil
}

// Delete 实现 RegistryBackend。
func (m *MemoryRegistryBackend) Delete(_ context.Context, runID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.records, runID)
	return nil
}

// BroadcastCancel 实现 RegistryBackend。
func (m *MemoryRegistryBackend) BroadcastCancel(_ context.Context, runID string) error {
	m.mu.Lock()
	subs := make([]chan string, 0, len(m.subs))
	for _, ch := range m.subs {
		subs = append(subs, ch)
	}
	m.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- runID:
		default:
			// 取消信号通道满时丢弃：取消是幂等的，客户端会重试。
			// 阻塞在这里会卡住调用方。
		}
	}
	return nil
}

// SubscribeCancel 实现 RegistryBackend。
func (m *MemoryRegistryBackend) SubscribeCancel(_ context.Context) (<-chan string, func(), error) {
	m.mu.Lock()
	id := m.next
	m.next++
	ch := make(chan string, 64)
	m.subs[id] = ch
	m.mu.Unlock()

	var once sync.Once
	stop := func() {
		once.Do(func() {
			m.mu.Lock()
			delete(m.subs, id)
			m.mu.Unlock()
			close(ch)
		})
	}
	return ch, stop, nil
}
