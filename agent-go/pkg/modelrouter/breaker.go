package modelrouter

import (
	"sync"
	"time"
)

// breakerState 是熔断器状态。
type breakerState int

const (
	stateClosed   breakerState = iota // 正常放行
	stateOpen                         // 熔断中，直接跳过
	stateHalfOpen                     // 半开探测
)

// breaker 是单个模型的熔断器。
//
// 按"模型"而不是"供应商"计：同一家的 fast 层挂了不代表 standard 层也挂了，
// 按供应商熔断会把可用的模型一起打掉。
type breaker struct {
	threshold int
	cooldown  time.Duration

	mu       sync.Mutex
	state    breakerState
	failures int
	openedAt time.Time
}

func newBreaker(threshold int, cooldown time.Duration) *breaker {
	if threshold <= 0 {
		threshold = 3
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &breaker{threshold: threshold, cooldown: cooldown}
}

// allow 报告是否放行本次调用。
func (b *breaker) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case stateClosed:
		return true

	case stateOpen:
		if now.Sub(b.openedAt) < b.cooldown {
			return false
		}
		// 冷却结束，放一个探测请求进去。
		b.state = stateHalfOpen
		return true

	default: // stateHalfOpen
		// 半开期只放一个探测：再放更多就是在已知可能不可用的后端上堆请求。
		return false
	}
}

// recordSuccess 记录一次成功。
func (b *breaker) recordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = stateClosed
	b.failures = 0
}

// recordFailure 记录一次失败。
func (b *breaker) recordFailure(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == stateHalfOpen {
		// 探测失败：立刻回到熔断并重新计时。
		b.state = stateOpen
		b.openedAt = now
		return
	}

	b.failures++
	if b.failures >= b.threshold {
		b.state = stateOpen
		b.openedAt = now
	}
}

// open 报告熔断器当前是否处于熔断态，供可观测性使用。
func (b *breaker) open() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state == stateOpen
}
