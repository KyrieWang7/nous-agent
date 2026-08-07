package message

import "sync"

// History 是一次 agent 回合的转录，也是回合的唯一状态（设计文档 §3.3）。
//
// 只有两种操作可以改写既有历史：
//   - Replace：压缩，把前半段换成摘要
//   - ReplaceLastAssistant：护栏撤回最后一条回复
//
// 其余操作只追加。History 是并发安全的：并发工具执行期间，
// 内核与中间件共享同一个 History 实例。
type History struct {
	mu   sync.RWMutex
	msgs []Message

	// appends 是累计 append 次数，作为水位（Watermark）的基准。
	//
	// 为什么不用切片下标：压缩会在一轮之内缩短转录，保存的下标会指向错误消息
	// 甚至越界。pace-grid 上因此丢过整轮问答 —— 持久化层拿 baseline 下标去切
	// history，压缩后 len(history) < baseline，切出空集，那一轮的问和答都没入库，
	// DB 与内存转录从此分叉。Replace 不重置 appends，正是这个语义的关键。
	appends uint64
}

// NewHistory 返回空转录。
func NewHistory() *History {
	return &History{}
}

// Load 从持久化恢复转录。恢复的消息不计入水位增量 ——
// 它们不属于"本回合新增"。
func (h *History) Load(msgs []Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.msgs = CloneAll(msgs)
	h.appends = 0
}

// Append 追加一条消息并推进水位。
func (h *History) Append(m Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.msgs = append(h.msgs, Clone(m))
	h.appends++
}

// AppendAll 追加多条消息，每条各推进水位一次。
func (h *History) AppendAll(msgs []Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range msgs {
		h.msgs = append(h.msgs, Clone(m))
		h.appends++
	}
}

// All 返回当前活跃转录的深拷贝。
func (h *History) All() []Message {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return CloneAll(h.msgs)
}

// Len 返回当前活跃转录的消息条数。
func (h *History) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.msgs)
}

// Watermark 返回当前水位。回合开始时取一次，回合结束时配合 Since 计算新增。
func (h *History) Watermark() uint64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.appends
}

// Since 返回自水位 wm 以来新增的消息。
//
// 实现是"从尾部回溯 (appends - wm) 条"，并按当前长度封顶：压缩缩短转录后，
// 回溯量可能超过现有长度，此时返回全部现存消息。这样压缩轮也能拿到本轮的
// 问答，持久化层再据此走整体重写路径（设计文档 §13.2）。
func (h *History) Since(wm uint64) []Message {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.appends <= wm {
		return nil
	}
	n := int(h.appends - wm)
	if n > len(h.msgs) {
		n = len(h.msgs)
	}
	if n == 0 {
		return nil
	}
	return CloneAll(h.msgs[len(h.msgs)-n:])
}

// Replace 用 msgs 整体替换活跃转录。压缩专用。
//
// 水位不动：它计的是 append 次数，不是长度。
func (h *History) Replace(msgs []Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.msgs = CloneAll(msgs)
}

// ReplaceLastAssistant 从尾部查找第一条 assistant 消息并替换，返回是否命中。
//
// 护栏专用。护栏判定发生在回复入库之后，若只改 ModelOutput，被拦截的文本会留在
// 持久化转录和下一轮请求里（设计文档 §3.3）。
func (h *History) ReplaceLastAssistant(m Message) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	for i := len(h.msgs) - 1; i >= 0; i-- {
		if h.msgs[i].Role == RoleAssistant {
			h.msgs[i] = Clone(m)
			return true
		}
	}
	return false
}

// TokenCount 返回当前转录的估算 token 数。
func (h *History) TokenCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return EstimateMessagesTokens(h.msgs)
}
