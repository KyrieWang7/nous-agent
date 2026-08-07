package runtime

import (
	"fmt"
	"sync"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

// Bucket 是用量归因的桶。
type Bucket string

const (
	// BucketLead 是主循环的模型消耗。
	BucketLead Bucket = "lead"

	// BucketSubagent 是派发出去的 worker 的消耗。
	BucketSubagent Bucket = "subagent"

	// BucketMiddleware 是系统开销：压缩摘要、标题生成这类旁路调用。
	BucketMiddleware Bucket = "middleware"
)

// Price 是某个模型的单价，单位是"每百万 token 的微单位成本"。
//
// 用整数微单位而不是浮点：一次 run 里累加几十次浮点会引入误差，
// 而账单不能有误差。
type Price struct {
	InputPerMillion  int64
	OutputPerMillion int64

	// CachedInputPerMillion 是缓存命中部分的单价，通常远低于 InputPerMillion。
	// 为 0 时按 InputPerMillion 计。
	CachedInputPerMillion int64
}

// Pricer 按模型名给出单价。
type Pricer struct {
	prices   map[string]Price
	fallback Price
}

// NewPricer 返回定价表。fallback 用于未列出的模型。
func NewPricer(prices map[string]Price, fallback Price) *Pricer {
	cloned := make(map[string]Price, len(prices))
	for k, v := range prices {
		cloned[k] = v
	}
	return &Pricer{prices: cloned, fallback: fallback}
}

// CostMicros 计算一次调用的成本（微单位）。
func (p *Pricer) CostMicros(modelName string, u model.Usage) int64 {
	if p == nil {
		return 0
	}

	price, ok := p.prices[modelName]
	if !ok {
		price = p.fallback
	}

	cachedRate := price.CachedInputPerMillion
	if cachedRate == 0 {
		cachedRate = price.InputPerMillion
	}

	// 缓存命中的部分不该按全价计：InputTokens 通常已含 CachedInputTokens，
	// 按全价算会高估成本，而高估会让额度提前熔断。
	fresh := int64(u.InputTokens - u.CachedInputTokens)
	if fresh < 0 {
		fresh = int64(u.InputTokens)
	}

	const million = 1_000_000
	cost := fresh*price.InputPerMillion/million +
		int64(u.CachedInputTokens)*cachedRate/million +
		int64(u.OutputTokens)*price.OutputPerMillion/million

	return cost
}

// Entry 是一次模型调用的用量记录。
type Entry struct {
	Bucket Bucket

	// Source 是桶内的细分来源，例如 subagent 名或中间件名。
	Source string

	// CallID 是模型调用的唯一标识，用于去重。
	//
	// 去重是必需的：流式路径上 done 事件与 Result() 都可能上报同一次调用，
	// 重复计数会让账单虚高（设计文档 §15）。
	CallID string

	ModelName string
	Usage     model.Usage
}

// Totals 是三桶归因的汇总。
type Totals struct {
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int

	LeadTokens       int
	SubagentTokens   int
	MiddlewareTokens int

	CostMicros int64

	// LLMCalls 是去重后的模型调用次数。
	LLMCalls int
}

// Journal 累积一次 run 的用量。并发安全：subagent 会并发回灌。
type Journal struct {
	pricer *Pricer

	mu     sync.Mutex
	seen   map[string]struct{}
	totals Totals

	// bySource 供可观测性使用："这个 run 的开销主要来自哪个 subagent"
	// 是运营必须能回答的问题。
	bySource map[string]int
	onChange func(Totals)
}

// NewJournal 返回用量记账本。
func NewJournal(p *Pricer) *Journal {
	return &Journal{
		pricer:   p,
		seen:     make(map[string]struct{}),
		bySource: make(map[string]int),
	}
}

// Observe 记录一次模型调用。重复的 CallID 被忽略并返回 false。
func (j *Journal) Observe(e Entry) bool {
	if j == nil {
		return false
	}

	key := dedupeKey(e)

	j.mu.Lock()

	if _, dup := j.seen[key]; dup {
		j.mu.Unlock()
		return false
	}
	j.seen[key] = struct{}{}

	tokens := e.Usage.TotalTokens()

	j.totals.InputTokens += e.Usage.InputTokens
	j.totals.OutputTokens += e.Usage.OutputTokens
	j.totals.CachedInputTokens += e.Usage.CachedInputTokens
	j.totals.LLMCalls++
	j.totals.CostMicros += j.pricer.CostMicros(e.ModelName, e.Usage)

	switch e.Bucket {
	case BucketSubagent:
		j.totals.SubagentTokens += tokens
	case BucketMiddleware:
		j.totals.MiddlewareTokens += tokens
	default:
		j.totals.LeadTokens += tokens
	}

	if e.Source != "" {
		j.bySource[string(e.Bucket)+":"+e.Source] += tokens
	}
	totals, onChange := j.totals, j.onChange
	j.mu.Unlock()
	if onChange != nil {
		onChange(totals)
	}
	return true
}

// dedupeKey 构造去重键。
//
// 键里带桶与来源：同一次模型调用在 subagent 的 journal 与父 journal 里
// 各记一次是**正确的** —— 前者是 worker 自己的账，后者是回灌到父 run 的账。
// 只按 CallID 去重会让回灌被当成重复而丢掉，于是 subagent_tokens 永远是 0
// （设计文档 §10.1）。
func dedupeKey(e Entry) string {
	if e.CallID == "" {
		// 没有 CallID 时无法去重，用一个不会碰撞的键让它照常计入。
		return fmt.Sprintf("%s:%s:nocallid:%d", e.Bucket, e.Source, len(e.ModelName))
	}
	return fmt.Sprintf("%s:%s:%s", e.Bucket, e.Source, e.CallID)
}

// Totals 返回当前汇总。
func (j *Journal) Totals() Totals {
	if j == nil {
		return Totals{}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.totals
}

// BySource 返回按来源细分的 token 数。
func (j *Journal) BySource() map[string]int {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	out := make(map[string]int, len(j.bySource))
	for k, v := range j.bySource {
		out[k] = v
	}
	return out
}

// Merge 把 subagent 的账本回灌到父账本的 subagent 桶。
//
// 不回灌的话 subagent_tokens 永远是 0，而且一个 run 可以靠不断派发
// 绕过成本上限（设计文档 §10.1）。
func (j *Journal) Merge(name string, child *Journal) {
	if j == nil || child == nil {
		return
	}

	t := child.Totals()
	j.mu.Lock()

	j.totals.InputTokens += t.InputTokens
	j.totals.OutputTokens += t.OutputTokens
	j.totals.CachedInputTokens += t.CachedInputTokens
	j.totals.LLMCalls += t.LLMCalls
	j.totals.CostMicros += t.CostMicros

	// 子账本里的一切都归父账本的 subagent 桶，包括子 agent 自己的
	// middleware 开销 —— 从父 run 的视角看，那都是"派发的代价"。
	j.totals.SubagentTokens += t.InputTokens + t.OutputTokens

	if name != "" {
		j.bySource[string(BucketSubagent)+":"+name] += t.InputTokens + t.OutputTokens
	}
	totals, onChange := j.totals, j.onChange
	j.mu.Unlock()
	if onChange != nil {
		onChange(totals)
	}
}

// SetOnChange installs a callback for future accounting changes. The callback
// runs without the journal lock and is intended for idempotent persistence of
// late asynchronous subagent usage.
func (j *Journal) SetOnChange(fn func(Totals)) {
	if j == nil {
		return
	}
	j.mu.Lock()
	j.onChange = fn
	j.mu.Unlock()
}
