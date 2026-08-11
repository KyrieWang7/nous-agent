package loop

import (
	"errors"
	"sync/atomic"
	"time"
)

// 内核级失败。它们对外分别表现为"任务过于复杂"、"处理超时"、"额度不足"。
var (
	ErrMaxIterations   = errors.New("loop: max iterations reached")
	ErrDeadline        = errors.New("loop: wall-clock deadline exceeded")
	ErrBudgetExhausted = errors.New("loop: cost budget exhausted")
)

// Limits 是内核的安全上限。
//
// 这些上限刻意放在内核而不是中间件：中间件的本质是"可以从链里删掉"，
// 而这几样一旦可删就等于可以被误配成无上限 —— 那是能烧穿账单的失效模式。
// LoopDetection 那类启发式检测可以是中间件（删了只是少一层保护），
// 但 iteration >= MaxIterations 这个判断必须钉死在 for 里（设计文档 §3.3）。
type Limits struct {
	// MaxIterations 是单次 run 的最大迭代轮数。<= 0 表示不限轮次。
	MaxIterations int

	// Deadline 是单次 run 的墙钟上限。<= 0 表示不限。
	// 即便 MaxIterations 为 0 也生效，否则卡在长工具上的 run 可以无限期烧下去。
	Deadline time.Duration

	// MaxCostMicros 是单次 run 的成本上限（整数微单位）。<= 0 表示不限。
	//
	// 判据是成本而不是 token：不同分层每 token 花费差几倍，
	// 按 token 限额会让 vision 与 fast 同价。
	MaxCostMicros int64

	// MaxTokens is the aggregate per-run token ceiling across the lead agent,
	// middleware model calls, and all child agents. <= 0 means unbounded.
	MaxTokens int

	// StopReinjectionLimit 是停止门拦截后的最大续跑次数。<= 0 时用默认值。
	//
	// 没有这个上限，一个总是不满意的 Stop hook 就是一台无限烧钱的机器。
	StopReinjectionLimit int
}

// defaultStopReinjectionLimit 是未配置时的续跑上限。
const defaultStopReinjectionLimit = 3

// Tracker 跟踪单次 run 的上限消耗。并发安全：成本可能由 subagent 并发回灌。
type Tracker struct {
	limits  Limits
	started time.Time
	cost    atomic.Int64
	tokens  atomic.Int64
}

// NewTracker 返回从此刻开始计时的 Tracker。
func (l Limits) NewTracker() *Tracker {
	return &Tracker{limits: l, started: time.Now()}
}

// ObserveCost 累加已发生的成本（微单位）。
func (t *Tracker) ObserveCost(micros int64) {
	t.cost.Add(micros)
}

// CostMicros 返回累计成本。
func (t *Tracker) CostMicros() int64 {
	return t.cost.Load()
}

// ObserveSpend imports a monotonic aggregate snapshot, typically from the
// run Journal shared by the lead and child agents.
func (t *Tracker) ObserveSpend(tokens, costMicros int64) {
	storeMax(&t.tokens, tokens)
	storeMax(&t.cost, costMicros)
}

func storeMax(dst *atomic.Int64, value int64) {
	for current := dst.Load(); value > current; current = dst.Load() {
		if dst.CompareAndSwap(current, value) {
			return
		}
	}
}

// Check 在每轮迭代开始时调用，iteration 是即将执行的轮次序号（从 0 起）。
//
// 检查顺序是刻意的：轮次上限最常见也最好懂，先报它能让绝大多数
// "任务过于复杂"的排查一步到位。
func (t *Tracker) Check(iteration int) error {
	if t.limits.MaxIterations > 0 && iteration >= t.limits.MaxIterations {
		return ErrMaxIterations
	}
	if t.limits.Deadline > 0 && time.Since(t.started) >= t.limits.Deadline {
		return ErrDeadline
	}
	if t.limits.MaxTokens > 0 && t.tokens.Load() >= int64(t.limits.MaxTokens) {
		return ErrBudgetExhausted
	}
	if t.limits.MaxCostMicros > 0 && t.cost.Load() > t.limits.MaxCostMicros {
		return ErrBudgetExhausted
	}
	return nil
}

// Elapsed 返回本次 run 已耗时。
func (t *Tracker) Elapsed() time.Duration {
	return time.Since(t.started)
}
