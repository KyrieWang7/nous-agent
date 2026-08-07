// Package modelrouter 实现分层、降级链、熔断与错误分类恢复。
//
// 它实现 loop.Sampler，对内核而言只是"一个会采样的东西"。内核不知道
// 有几个模型、谁降级到谁 —— 这些是路由策略，不是编排（设计文档 §11.2）。
package modelrouter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

// Tier 是模型分层。
type Tier string

const (
	// TierFast 用于标题生成、意图识别、上下文压缩这类系统开销。
	TierFast Tier = "fast"

	// TierStandard 是主循环的默认层。
	TierStandard Tier = "standard"

	// TierVision 用于图像分析。
	TierVision Tier = "vision"
)

// ErrUnavailable 表示降级链已走完，没有可用模型。
var ErrUnavailable = errors.New("modelrouter: no model available")

// Compactor 在上下文超限时强制压缩转录。
//
// 只声明需要的那一个方法而不是复用 loop.Compactor：本包不该为了一个
// 方法去依赖内核包（layercheck 会拦住）。
type Compactor interface {
	MaybeCompact(ctx context.Context, h *message.History) (bool, error)
}

// Config 配置路由器。
type Config struct {
	// Models 是每一层的模型。至少要有 TierStandard。
	Models map[Tier]model.Model

	// NamedModels are user-selectable models. The transport places model_name
	// in State.Values; the router resolves it here so model selection remains a
	// runtime concern instead of leaking into the loop kernel.
	NamedModels map[string]model.Model

	// Fallbacks 是每一层的降级顺序。未声明时用 [该层, standard, fast]。
	Fallbacks map[Tier][]Tier

	// Stream 为真时走流式采样。
	Stream bool

	// OnStreamEvent observes provider deltas before the response is assembled.
	// It must be non-blocking; delivery failures must not fail model sampling.
	OnStreamEvent func(context.Context, *middleware.State, model.StreamEvent)

	// Compactor 用于上下文超限后的强制压缩重发。为 nil 时超限不重试。
	Compactor Compactor

	// MaxContextRetries 是上下文超限后的最大重发次数。<= 0 时用 2。
	MaxContextRetries int

	// MaxRateLimitRetries 是限流退避重试次数。<= 0 时用 3。
	MaxRateLimitRetries int

	// BaseBackoff 是限流退避的基准间隔。<= 0 时用 500ms。
	BaseBackoff time.Duration

	// BreakerThreshold 是熔断的连续失败阈值。<= 0 时用 3。
	BreakerThreshold int

	// BreakerCooldown 是熔断的冷却时长。<= 0 时用 30s。
	BreakerCooldown time.Duration

	// Sleep 用于测试注入，默认 time.Sleep 的可取消版本。
	Sleep func(ctx context.Context, d time.Duration) error

	// Now 用于测试注入。
	Now func() time.Time

	Logger *slog.Logger
}

// Router 按层选择模型并处理失败恢复。
type Router struct {
	cfg Config

	mu       sync.Mutex
	breakers map[string]*breaker

	logger *slog.Logger
}

// New 校验配置并返回路由器。
func New(cfg Config) (*Router, error) {
	if len(cfg.Models) == 0 {
		return nil, errors.New("modelrouter: at least one model is required")
	}
	if _, ok := cfg.Models[TierStandard]; !ok {
		return nil, fmt.Errorf("modelrouter: tier %q is required as the default", TierStandard)
	}
	for tier, m := range cfg.Models {
		if m == nil {
			return nil, fmt.Errorf("modelrouter: tier %q has a nil model", tier)
		}
	}
	for name, m := range cfg.NamedModels {
		if name == "" {
			return nil, errors.New("modelrouter: named model requires a name")
		}
		if m == nil {
			return nil, fmt.Errorf("modelrouter: named model %q is nil", name)
		}
	}

	if cfg.MaxContextRetries <= 0 {
		cfg.MaxContextRetries = 2
	}
	if cfg.MaxRateLimitRetries <= 0 {
		cfg.MaxRateLimitRetries = 3
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 500 * time.Millisecond
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepCtx
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	return &Router{cfg: cfg, breakers: make(map[string]*breaker), logger: cfg.Logger}, nil
}

// Sample 实现 loop.Sampler。
//
// 恢复策略（设计文档 §11.2）：
//   - 上下文超限 → 强制压缩后重发，上限 MaxContextRetries，不计入熔断失败
//   - 限流 → 指数退避重试，不立即降级
//   - 供应商不可用 → 沿降级链换下一层
//   - 参数错误 / 认证失败 → 直接返回，重试没有意义
//
// **已流出内容的请求不重试**：客户端已经看到了半截回答，重发会让它看到两段。
func (r *Router) Sample(ctx context.Context, st *middleware.State) (*model.Response, bool, error) {
	if st.ModelInput == nil {
		return nil, false, errors.New("modelrouter: model input was not built")
	}
	if name := selectedModelName(st); name != "" {
		return r.sampleNamed(ctx, name, st)
	}

	tier := r.tierFor(st)
	chain := r.chainFor(tier)

	var lastErr error
	for _, candidate := range chain {
		m, ok := r.cfg.Models[candidate]
		if !ok {
			continue
		}

		b := r.breakerFor(string(candidate))
		if !b.allow(r.cfg.Now()) {
			lastErr = fmt.Errorf("%w: tier %q circuit is open", ErrUnavailable, candidate)
			continue
		}

		resp, streamed, err := r.sampleWithRecovery(ctx, m, st)
		switch {
		case err == nil:
			b.recordSuccess()
			return resp, streamed, nil

		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			// 取消是外部意志，不是后端的错，也不该降级重试。
			return nil, streamed, err

		case streamed:
			// 已流出内容的请求不重试也不降级。
			b.recordFailure(r.cfg.Now())
			return nil, true, err

		case model.IsProviderUnavailable(err), model.IsRateLimited(err):
			b.recordFailure(r.cfg.Now())
			lastErr = err
			r.logger.WarnContext(ctx, "model tier failed, trying the next one",
				"tier", candidate, "error", err)
			continue

		default:
			// 参数错误、认证失败、上下文超限重试耗尽：换个后端也是一样的结果。
			b.recordFailure(r.cfg.Now())
			return nil, streamed, err
		}
	}

	if lastErr == nil {
		lastErr = ErrUnavailable
	}
	return nil, false, fmt.Errorf("%w: %w", ErrUnavailable, lastErr)
}

func (r *Router) sampleNamed(ctx context.Context, name string, st *middleware.State) (*model.Response, bool, error) {
	m, ok := r.cfg.NamedModels[name]
	if !ok {
		available := make([]string, 0, len(r.cfg.NamedModels))
		for candidate := range r.cfg.NamedModels {
			available = append(available, candidate)
		}
		sort.Strings(available)
		return nil, false, fmt.Errorf("modelrouter: unknown model %q; available: %v", name, available)
	}

	b := r.breakerFor("model:" + name)
	if !b.allow(r.cfg.Now()) {
		return nil, false, fmt.Errorf("%w: model %q circuit is open", ErrUnavailable, name)
	}
	resp, streamed, err := r.sampleWithRecovery(ctx, m, st)
	if err == nil {
		b.recordSuccess()
		return resp, streamed, nil
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		b.recordFailure(r.cfg.Now())
	}
	return nil, streamed, err
}

func selectedModelName(st *middleware.State) string {
	if st == nil || st.Values == nil {
		return ""
	}
	name, _ := st.Values["model_name"].(string)
	return name
}

// sampleWithRecovery 在单个模型上完成限流退避与上下文超限重发。
func (r *Router) sampleWithRecovery(
	ctx context.Context, m model.Model, st *middleware.State,
) (*model.Response, bool, error) {
	var (
		rateRetries    int
		contextRetries int
	)

	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}

		resp, streamed, err := r.sampleOnce(ctx, m, st)
		if err == nil {
			return resp, streamed, nil
		}
		if streamed {
			return nil, true, err
		}

		switch {
		case model.IsRateLimited(err) && rateRetries < r.cfg.MaxRateLimitRetries:
			rateRetries++
			backoff := r.cfg.BaseBackoff << (rateRetries - 1)
			r.logger.WarnContext(ctx, "rate limited, backing off",
				"attempt", rateRetries, "backoff", backoff)
			if serr := r.cfg.Sleep(ctx, backoff); serr != nil {
				return nil, false, serr
			}

		case model.IsContextOverflow(err) && contextRetries < r.cfg.MaxContextRetries:
			contextRetries++
			if r.cfg.Compactor == nil || st.History == nil {
				return nil, false, err
			}
			compacted, cerr := r.cfg.Compactor.MaybeCompact(ctx, st.History)
			if cerr != nil {
				return nil, false, cerr
			}
			if !compacted {
				// 压不动了还超限：再试也是一样，别白烧一次调用。
				return nil, false, err
			}
			st.Compacted = true
			r.rebuildInput(st)
			r.logger.WarnContext(ctx, "context overflow, recompacted and retrying",
				"attempt", contextRetries)

		default:
			return nil, false, err
		}
	}
}

// rebuildInput 在压缩后刷新投递给模型的消息。
//
// 不刷新的话重发的还是那份超限的消息，压缩等于白做。
func (r *Router) rebuildInput(st *middleware.State) {
	if st.ModelInput == nil || st.History == nil {
		return
	}
	req := *st.ModelInput
	req.Messages = st.History.All()
	st.ModelInput = &req
}

// sampleOnce 做一次真实调用。第二个返回值表示是否已有内容流出。
func (r *Router) sampleOnce(
	ctx context.Context, m model.Model, st *middleware.State,
) (*model.Response, bool, error) {
	if !r.cfg.Stream {
		resp, err := m.Complete(ctx, *st.ModelInput)
		if err != nil {
			return nil, false, err
		}
		if resp == nil {
			return nil, false, errors.New("modelrouter: model returned no response")
		}
		return resp, false, nil
	}

	reader, err := m.Stream(ctx, *st.ModelInput)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = reader.Close() }()

	// streamed 一旦置位就不能重试：客户端已经看到内容了。
	streamed := false
	for {
		ev, ok := reader.Next()
		if !ok {
			break
		}
		if ev.Type == model.StreamTextDelta && ev.Delta != "" {
			streamed = true
		}
		if r.cfg.OnStreamEvent != nil {
			r.cfg.OnStreamEvent(ctx, st, ev)
		}
	}

	resp, err := reader.Result()
	if err != nil {
		return nil, streamed, err
	}
	if resp == nil {
		return nil, streamed, errors.New("modelrouter: stream produced no response")
	}
	return resp, streamed, nil
}

// tierFor 决定本次采样用哪一层。
func (r *Router) tierFor(st *middleware.State) Tier {
	// 有图片输入且配置了 vision 层时走 vision：让一个不支持视觉的模型
	// 去看图，得到的是一句"我看不到图片"，白花一次调用。
	if _, ok := r.cfg.Models[TierVision]; ok && hasImage(st) {
		return TierVision
	}
	return TierStandard
}

func hasImage(st *middleware.State) bool {
	if st.ModelInput == nil {
		return false
	}
	for _, m := range st.ModelInput.Messages {
		for _, b := range m.ContentBlocks {
			if b.Type == "image" {
				return true
			}
		}
	}
	return false
}

// chainFor 返回某一层的降级顺序。
func (r *Router) chainFor(tier Tier) []Tier {
	if chain, ok := r.cfg.Fallbacks[tier]; ok && len(chain) > 0 {
		return chain
	}

	// 默认链：本层 → standard → fast。去重后返回。
	out := []Tier{tier}
	for _, t := range []Tier{TierStandard, TierFast} {
		if t != tier {
			out = append(out, t)
		}
	}
	return out
}

func (r *Router) breakerFor(key string) *breaker {
	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.breakers[key]
	if !ok {
		b = newBreaker(r.cfg.BreakerThreshold, r.cfg.BreakerCooldown)
		r.breakers[key] = b
	}
	return b
}

// BreakerOpen 报告某一层是否处于熔断态，供可观测性使用。
func (r *Router) BreakerOpen(tier Tier) bool {
	return r.breakerFor(string(tier)).open()
}

// ModelFor 返回某一层的模型，供压缩、标题生成这类旁路调用使用。
//
// 它们该走 fast 层：用主模型做系统开销等于每次都付一次高价推理的钱。
func (r *Router) ModelFor(tier Tier) (model.Model, bool) {
	m, ok := r.cfg.Models[tier]
	return m, ok
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
