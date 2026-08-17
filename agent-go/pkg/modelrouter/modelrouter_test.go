package modelrouter_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/modelrouter"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
)

// newState 构造一个已经组装好 ModelInput 的 State。
func newState(msgs ...message.Message) *lifecycle.State {
	h := message.NewHistory()
	h.Load(msgs)

	st := lifecycle.NewState(lifecycle.StateInit{History: h})
	st.ModelInput = &model.Request{Messages: h.All()}
	return st
}

func user(content string) message.Message {
	return message.Message{Role: message.RoleUser, Content: content}
}

func mustRouter(t *testing.T, cfg modelrouter.Config) *modelrouter.Router {
	t.Helper()

	if cfg.Sleep == nil {
		cfg.Sleep = func(context.Context, time.Duration) error { return nil } // 测试不真睡
	}
	r, err := modelrouter.New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return r
}

// --- 配置校验 ---

func TestNew_RequiresStandardTier(t *testing.T) {
	t.Parallel()

	_, err := modelrouter.New(modelrouter.Config{
		Models: map[modelrouter.Tier]model.Model{modelrouter.TierFast: faux.New()},
	})
	if err == nil {
		t.Fatal("New() succeeded without a standard tier")
	}
}

func TestNew_RejectsEmptyAndNilModels(t *testing.T) {
	t.Parallel()

	if _, err := modelrouter.New(modelrouter.Config{}); err == nil {
		t.Fatal("New() succeeded with no models")
	}

	_, err := modelrouter.New(modelrouter.Config{
		Models: map[modelrouter.Tier]model.Model{modelrouter.TierStandard: nil},
	})
	if err == nil {
		t.Fatal("New() accepted a nil model")
	}
}

// --- 基本采样 ---

func TestSample_UsesStandardTier(t *testing.T) {
	t.Parallel()

	std := faux.New(faux.Text("from standard"))
	fast := faux.New(faux.Text("from fast"))

	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{
		modelrouter.TierStandard: std,
		modelrouter.TierFast:     fast,
	}})

	got, streamed, err := r.Sample(context.Background(), newState(user("hi")))
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if streamed {
		t.Error("non-streaming sample reported streamed = true")
	}
	if got.Message.Content != "from standard" {
		t.Fatalf("content = %q", got.Message.Content)
	}
	if fast.CallCount() != 0 {
		t.Error("the fast tier was called when standard succeeded")
	}
}

func TestSample_RequiresModelInput(t *testing.T) {
	t.Parallel()

	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{
		modelrouter.TierStandard: faux.New(faux.Text("x")),
	}})

	st := lifecycle.NewState(lifecycle.StateInit{History: message.NewHistory()})
	if _, _, err := r.Sample(context.Background(), st); err == nil {
		t.Fatal("Sample() without a model input succeeded")
	}
}

// 有图片输入时走 vision 层：让不支持视觉的模型看图只会得到"我看不到图片"。
func TestSample_RoutesImagesToVisionTier(t *testing.T) {
	t.Parallel()

	std := faux.New(faux.Text("standard"))
	vision := faux.New(faux.Text("vision"))

	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{
		modelrouter.TierStandard: std,
		modelrouter.TierVision:   vision,
	}})

	st := newState(message.Message{
		Role:          message.RoleUser,
		Content:       "what is this?",
		ContentBlocks: []message.ContentBlock{{Type: "image", Data: "AAAA"}},
	})

	got, _, err := r.Sample(context.Background(), st)
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if got.Message.Content != "vision" {
		t.Fatalf("content = %q, want the vision tier to serve an image request", got.Message.Content)
	}
}

func TestSample_TextOnlyStaysOnStandardEvenWithVisionConfigured(t *testing.T) {
	t.Parallel()

	std := faux.New(faux.Text("standard"))
	vision := faux.New(faux.Text("vision"))

	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{
		modelrouter.TierStandard: std,
		modelrouter.TierVision:   vision,
	}})

	got, _, err := r.Sample(context.Background(), newState(user("plain text")))
	if err != nil {
		t.Fatal(err)
	}
	if got.Message.Content != "standard" {
		t.Fatalf("content = %q; a text request must not burn the vision tier", got.Message.Content)
	}
}

// --- 降级链 ---

func TestSample_FallsBackWhenProviderUnavailable(t *testing.T) {
	t.Parallel()

	std := faux.New(faux.Fail(model.ErrProviderUnavailable))
	fast := faux.New(faux.Text("rescued by fast"))

	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{
		modelrouter.TierStandard: std,
		modelrouter.TierFast:     fast,
	}})

	got, _, err := r.Sample(context.Background(), newState(user("hi")))
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if got.Message.Content != "rescued by fast" {
		t.Fatalf("content = %q", got.Message.Content)
	}
}

func TestSample_ExhaustedChainReturnsUnavailable(t *testing.T) {
	t.Parallel()

	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{
		modelrouter.TierStandard: faux.New(faux.Fail(model.ErrProviderUnavailable)),
		modelrouter.TierFast:     faux.New(faux.Fail(model.ErrProviderUnavailable)),
	}})

	_, _, err := r.Sample(context.Background(), newState(user("hi")))
	if !errors.Is(err, modelrouter.ErrUnavailable) {
		t.Fatalf("Sample() error = %v, want ErrUnavailable", err)
	}
}

func TestSample_CustomFallbackChain(t *testing.T) {
	t.Parallel()

	std := faux.New(faux.Fail(model.ErrProviderUnavailable))
	fast := faux.New(faux.Text("should not be reached"))

	// 显式声明 standard 不降级
	r := mustRouter(t, modelrouter.Config{
		Models: map[modelrouter.Tier]model.Model{
			modelrouter.TierStandard: std,
			modelrouter.TierFast:     fast,
		},
		Fallbacks: map[modelrouter.Tier][]modelrouter.Tier{
			modelrouter.TierStandard: {modelrouter.TierStandard},
		},
	})

	if _, _, err := r.Sample(context.Background(), newState(user("hi"))); err == nil {
		t.Fatal("Sample() succeeded despite a single-tier chain with a failing model")
	}
	if fast.CallCount() != 0 {
		t.Error("the custom chain was ignored and the fast tier was used")
	}
}

// 参数错误换个后端也是一样的结果，不该降级。
func TestSample_DoesNotFallBackOnInvalidRequest(t *testing.T) {
	t.Parallel()

	std := faux.New(faux.Fail(model.ErrInvalidRequest))
	fast := faux.New(faux.Text("should not be reached"))

	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{
		modelrouter.TierStandard: std,
		modelrouter.TierFast:     fast,
	}})

	_, _, err := r.Sample(context.Background(), newState(user("hi")))
	if err == nil {
		t.Fatal("Sample() succeeded on an invalid request")
	}
	if fast.CallCount() != 0 {
		t.Error("an invalid request triggered a pointless fallback")
	}
}

func TestSample_DoesNotFallBackOnAuthFailure(t *testing.T) {
	t.Parallel()

	fast := faux.New(faux.Text("should not be reached"))
	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{
		modelrouter.TierStandard: faux.New(faux.Fail(model.ErrAuthFailed)),
		modelrouter.TierFast:     fast,
	}})

	if _, _, err := r.Sample(context.Background(), newState(user("hi"))); err == nil {
		t.Fatal("Sample() succeeded with a bad key")
	}
	if fast.CallCount() != 0 {
		t.Error("an auth failure triggered a fallback; the next tier would fail the same way")
	}
}

// --- 限流退避 ---

func TestSample_RetriesOnRateLimitWithoutFallingBack(t *testing.T) {
	t.Parallel()

	std := faux.New(
		faux.Fail(model.ErrRateLimited),
		faux.Fail(model.ErrRateLimited),
		faux.Text("succeeded after backoff"),
	)
	fast := faux.New(faux.Text("should not be reached"))

	var slept []time.Duration
	r := mustRouter(t, modelrouter.Config{
		Models: map[modelrouter.Tier]model.Model{
			modelrouter.TierStandard: std,
			modelrouter.TierFast:     fast,
		},
		BaseBackoff: 10 * time.Millisecond,
		Sleep: func(_ context.Context, d time.Duration) error {
			slept = append(slept, d)
			return nil
		},
	})

	got, _, err := r.Sample(context.Background(), newState(user("hi")))
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if got.Message.Content != "succeeded after backoff" {
		t.Fatalf("content = %q", got.Message.Content)
	}
	if fast.CallCount() != 0 {
		t.Error("rate limiting triggered a fallback instead of a backoff retry")
	}
	if len(slept) != 2 {
		t.Fatalf("slept %d times, want 2", len(slept))
	}
	// 指数退避：第二次必须比第一次长
	if slept[1] <= slept[0] {
		t.Errorf("backoff did not grow: %v then %v", slept[0], slept[1])
	}
}

func TestSample_RateLimitRetriesAreBounded(t *testing.T) {
	t.Parallel()

	turns := make([]faux.Turn, 10)
	for i := range turns {
		turns[i] = faux.Fail(model.ErrRateLimited)
	}
	std := faux.New(turns...)

	r := mustRouter(t, modelrouter.Config{
		Models:              map[modelrouter.Tier]model.Model{modelrouter.TierStandard: std},
		MaxRateLimitRetries: 2,
		Fallbacks: map[modelrouter.Tier][]modelrouter.Tier{
			modelrouter.TierStandard: {modelrouter.TierStandard},
		},
	})

	if _, _, err := r.Sample(context.Background(), newState(user("hi"))); err == nil {
		t.Fatal("Sample() succeeded despite persistent rate limiting")
	}
	// 首次 + 2 次重试
	if got := std.CallCount(); got != 3 {
		t.Fatalf("model called %d times, want 3", got)
	}
}

func TestSample_BackoffRespectsCancellation(t *testing.T) {
	t.Parallel()

	r := mustRouter(t, modelrouter.Config{
		Models: map[modelrouter.Tier]model.Model{
			modelrouter.TierStandard: faux.New(faux.Fail(model.ErrRateLimited)),
		},
		Sleep: func(context.Context, time.Duration) error { return context.Canceled },
	})

	_, _, err := r.Sample(context.Background(), newState(user("hi")))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Sample() error = %v, want context.Canceled from the backoff", err)
	}
}

// --- 上下文超限恢复 ---

func TestSample_RecompactsAndRetriesOnContextOverflow(t *testing.T) {
	t.Parallel()

	std := faux.New(
		faux.Fail(model.ErrContextOverflow),
		faux.Text("fits now"),
	)

	c := &fakeCompactor{compacted: true}
	r := mustRouter(t, modelrouter.Config{
		Models:    map[modelrouter.Tier]model.Model{modelrouter.TierStandard: std},
		Compactor: c,
	})

	st := newState(user("a very long conversation"))
	got, _, err := r.Sample(context.Background(), st)
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if got.Message.Content != "fits now" {
		t.Fatalf("content = %q", got.Message.Content)
	}
	if c.calls != 1 {
		t.Fatalf("compactor called %d times, want 1", c.calls)
	}
	if !st.Compacted {
		t.Error("State.Compacted was not set; the persistence layer needs it to rewrite the transcript")
	}
}

// 压缩后必须刷新投递的消息，否则重发的还是那份超限内容，压缩白做。
func TestSample_RebuildsInputAfterRecompaction(t *testing.T) {
	t.Parallel()

	std := faux.New(faux.Fail(model.ErrContextOverflow), faux.Text("ok"))

	c := &fakeCompactor{compacted: true, replaceWith: []message.Message{user("compacted summary")}}
	r := mustRouter(t, modelrouter.Config{
		Models:    map[modelrouter.Tier]model.Model{modelrouter.TierStandard: std},
		Compactor: c,
	})

	st := newState(user("one"), user("two"), user("three"))
	if _, _, err := r.Sample(context.Background(), st); err != nil {
		t.Fatal(err)
	}

	reqs := std.Requests()
	if len(reqs) != 2 {
		t.Fatalf("model called %d times, want 2", len(reqs))
	}
	if len(reqs[1].Messages) != 1 || reqs[1].Messages[0].Content != "compacted summary" {
		t.Fatalf("retry sent %d messages (%+v); it must use the compacted transcript",
			len(reqs[1].Messages), reqs[1].Messages)
	}
}

// 压不动了还超限：再试也是一样，别白烧一次调用。
func TestSample_StopsWhenCompactionCannotShrinkFurther(t *testing.T) {
	t.Parallel()

	std := faux.New(faux.Fail(model.ErrContextOverflow), faux.Text("unreachable"))

	c := &fakeCompactor{compacted: false}
	r := mustRouter(t, modelrouter.Config{
		Models:    map[modelrouter.Tier]model.Model{modelrouter.TierStandard: std},
		Compactor: c,
		Fallbacks: map[modelrouter.Tier][]modelrouter.Tier{
			modelrouter.TierStandard: {modelrouter.TierStandard},
		},
	})

	_, _, err := r.Sample(context.Background(), newState(user("hi")))
	if !model.IsContextOverflow(err) {
		t.Fatalf("Sample() error = %v, want the overflow to surface", err)
	}
	if std.CallCount() != 1 {
		t.Fatalf("model called %d times, want 1; a no-op compaction must not trigger a retry", std.CallCount())
	}
}

func TestSample_ContextRetriesAreBounded(t *testing.T) {
	t.Parallel()

	turns := make([]faux.Turn, 10)
	for i := range turns {
		turns[i] = faux.Fail(model.ErrContextOverflow)
	}

	r := mustRouter(t, modelrouter.Config{
		Models:            map[modelrouter.Tier]model.Model{modelrouter.TierStandard: faux.New(turns...)},
		Compactor:         &fakeCompactor{compacted: true},
		MaxContextRetries: 1,
		Fallbacks: map[modelrouter.Tier][]modelrouter.Tier{
			modelrouter.TierStandard: {modelrouter.TierStandard},
		},
	})

	if _, _, err := r.Sample(context.Background(), newState(user("hi"))); err == nil {
		t.Fatal("Sample() succeeded despite persistent overflow")
	}
}

func TestSample_OverflowWithoutCompactorSurfaces(t *testing.T) {
	t.Parallel()

	r := mustRouter(t, modelrouter.Config{
		Models: map[modelrouter.Tier]model.Model{
			modelrouter.TierStandard: faux.New(faux.Fail(model.ErrContextOverflow)),
		},
		Fallbacks: map[modelrouter.Tier][]modelrouter.Tier{
			modelrouter.TierStandard: {modelrouter.TierStandard},
		},
	})

	if _, _, err := r.Sample(context.Background(), newState(user("hi"))); !model.IsContextOverflow(err) {
		t.Fatalf("Sample() error = %v, want the overflow to surface", err)
	}
}

// --- 熔断 ---

func TestBreaker_OpensAfterThresholdAndSkipsTier(t *testing.T) {
	t.Parallel()

	turns := make([]faux.Turn, 10)
	for i := range turns {
		turns[i] = faux.Fail(model.ErrProviderUnavailable)
	}
	std := faux.New(turns...)
	fast := faux.New(faux.Text("a"), faux.Text("b"), faux.Text("c"), faux.Text("d"))

	now := time.Now()
	r := mustRouter(t, modelrouter.Config{
		Models: map[modelrouter.Tier]model.Model{
			modelrouter.TierStandard: std,
			modelrouter.TierFast:     fast,
		},
		BreakerThreshold: 2,
		BreakerCooldown:  time.Minute,
		Now:              func() time.Time { return now },
	})

	// 前两次触发熔断
	for range 2 {
		if _, _, err := r.Sample(context.Background(), newState(user("hi"))); err != nil {
			t.Fatalf("Sample() error = %v", err)
		}
	}
	if !r.BreakerOpen(modelrouter.TierStandard) {
		t.Fatal("the breaker did not open after reaching the threshold")
	}

	callsBefore := std.CallCount()
	if _, _, err := r.Sample(context.Background(), newState(user("hi"))); err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if std.CallCount() != callsBefore {
		t.Fatal("the open breaker did not skip the failing tier")
	}
}

func TestBreaker_HalfOpenProbeRecovers(t *testing.T) {
	t.Parallel()

	std := faux.New(
		faux.Fail(model.ErrProviderUnavailable),
		faux.Fail(model.ErrProviderUnavailable),
		faux.Text("recovered"),
	)
	fast := faux.New(faux.Text("a"), faux.Text("b"))

	now := time.Now()
	r := mustRouter(t, modelrouter.Config{
		Models: map[modelrouter.Tier]model.Model{
			modelrouter.TierStandard: std,
			modelrouter.TierFast:     fast,
		},
		BreakerThreshold: 2,
		BreakerCooldown:  time.Minute,
		Now:              func() time.Time { return now },
	})

	for range 2 {
		if _, _, err := r.Sample(context.Background(), newState(user("hi"))); err != nil {
			t.Fatal(err)
		}
	}
	if !r.BreakerOpen(modelrouter.TierStandard) {
		t.Fatal("the breaker should be open")
	}

	// 冷却结束后放一个探测进去
	now = now.Add(2 * time.Minute)

	got, _, err := r.Sample(context.Background(), newState(user("hi")))
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if got.Message.Content != "recovered" {
		t.Fatalf("content = %q, want the half-open probe to reach standard", got.Message.Content)
	}
	if r.BreakerOpen(modelrouter.TierStandard) {
		t.Fatal("a successful probe must close the breaker")
	}
}

func TestBreaker_SuccessResetsFailureCount(t *testing.T) {
	t.Parallel()

	std := faux.New(
		faux.Fail(model.ErrProviderUnavailable),
		faux.Text("ok"),
		faux.Fail(model.ErrProviderUnavailable),
		faux.Text("ok again"),
	)
	fast := faux.New(faux.Text("fallback"), faux.Text("fallback"))

	now := time.Now()
	r := mustRouter(t, modelrouter.Config{
		Models: map[modelrouter.Tier]model.Model{
			modelrouter.TierStandard: std,
			modelrouter.TierFast:     fast,
		},
		BreakerThreshold: 2,
		BreakerCooldown:  time.Minute,
		Now:              func() time.Time { return now },
	})

	for range 4 {
		if _, _, err := r.Sample(context.Background(), newState(user("hi"))); err != nil {
			t.Fatalf("Sample() error = %v", err)
		}
	}

	// 失败被成功隔开，从未连续达到阈值
	if r.BreakerOpen(modelrouter.TierStandard) {
		t.Fatal("the breaker opened on non-consecutive failures")
	}
}

// 按模型而不是按供应商熔断：同一家的 fast 挂了不代表 standard 也挂了。
func TestBreaker_IsPerTier(t *testing.T) {
	t.Parallel()

	now := time.Now()
	r := mustRouter(t, modelrouter.Config{
		Models: map[modelrouter.Tier]model.Model{
			modelrouter.TierStandard: faux.New(faux.Text("a"), faux.Text("b"), faux.Text("c")),
			modelrouter.TierFast:     faux.New(faux.Fail(model.ErrProviderUnavailable)),
		},
		BreakerThreshold: 1,
		BreakerCooldown:  time.Minute,
		Now:              func() time.Time { return now },
	})

	for range 3 {
		if _, _, err := r.Sample(context.Background(), newState(user("hi"))); err != nil {
			t.Fatal(err)
		}
	}
	if r.BreakerOpen(modelrouter.TierStandard) {
		t.Fatal("the standard breaker opened even though standard always succeeded")
	}
}

// --- 流式 ---

func TestSample_StreamingReportsStreamedFlag(t *testing.T) {
	t.Parallel()

	r := mustRouter(t, modelrouter.Config{
		Models: map[modelrouter.Tier]model.Model{
			modelrouter.TierStandard: faux.New(faux.Text("streamed answer")),
		},
		Stream: true,
	})

	got, streamed, err := r.Sample(context.Background(), newState(user("hi")))
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if !streamed {
		t.Fatal("streamed = false after content was emitted")
	}
	if got.Message.Content != "streamed answer" {
		t.Fatalf("content = %q", got.Message.Content)
	}
}

func TestSample_StreamingObserverReceivesDeltas(t *testing.T) {
	var deltas []string
	r := mustRouter(t, modelrouter.Config{
		Models: map[modelrouter.Tier]model.Model{
			modelrouter.TierStandard: faux.New(faux.Text("abcdefgh")),
		},
		Stream: true,
		OnStreamEvent: func(_ context.Context, _ *lifecycle.State, ev model.StreamEvent) {
			if ev.Type == model.StreamTextDelta {
				deltas = append(deltas, ev.Delta)
			}
		},
	})
	if _, _, err := r.Sample(context.Background(), newState(user("hi"))); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(deltas, ""); got != "abcdefgh" {
		t.Fatalf("observed deltas = %q", got)
	}
}

// 已流出内容的请求不重试也不降级：客户端已经看到半截回答，重发会让它看到两段。
func TestSample_DoesNotRetryAfterBytesStreamed(t *testing.T) {
	t.Parallel()

	// 该回合会流出文本，然后在 Result 处失败
	std := faux.New(faux.Text("partial answer").WithStopReason(model.StopReasonStop))
	fast := faux.New(faux.Text("should not be reached"))

	r := mustRouter(t, modelrouter.Config{
		Models: map[modelrouter.Tier]model.Model{
			modelrouter.TierStandard: streamFailAfterDeltas{inner: std},
			modelrouter.TierFast:     fast,
		},
		Stream: true,
	})

	_, streamed, err := r.Sample(context.Background(), newState(user("hi")))
	if err == nil {
		t.Fatal("Sample() succeeded despite a stream failure")
	}
	if !streamed {
		t.Error("streamed = false; the caller needs to know content already went out")
	}
	if fast.CallCount() != 0 {
		t.Error("a partially streamed response was retried on another tier; the client would see two answers")
	}
}

func TestSample_DoesNotRetryAfterThinkingStreamed(t *testing.T) {
	t.Parallel()

	standard := faux.New(faux.Thinking("private reasoning", ""))
	fast := faux.New(faux.Text("should not be reached"))
	r := mustRouter(t, modelrouter.Config{
		Models: map[modelrouter.Tier]model.Model{
			modelrouter.TierStandard: streamFailAfterDeltas{inner: standard},
			modelrouter.TierFast:     fast,
		},
		Stream: true,
	})

	_, streamed, err := r.Sample(context.Background(), newState(user("hi")))
	if err == nil {
		t.Fatal("Sample() succeeded despite a stream failure")
	}
	if !streamed {
		t.Error("streamed = false after a reasoning delta was published")
	}
	if fast.CallCount() != 0 {
		t.Error("a response with streamed reasoning was retried on another tier")
	}
}

// --- 取消 ---

func TestSample_CancellationIsNotTreatedAsProviderFailure(t *testing.T) {
	t.Parallel()

	fast := faux.New(faux.Text("should not be reached"))
	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{
		modelrouter.TierStandard: faux.New(faux.Text("x")),
		modelrouter.TierFast:     fast,
	}})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := r.Sample(ctx, newState(user("hi")))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Sample() error = %v, want context.Canceled", err)
	}
	if fast.CallCount() != 0 {
		t.Error("cancellation triggered a fallback")
	}
}

// --- ModelFor ---

func TestModelFor(t *testing.T) {
	t.Parallel()

	fast := faux.New(faux.Text("x"))
	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{
		modelrouter.TierStandard: faux.New(faux.Text("y")),
		modelrouter.TierFast:     fast,
	}})

	// 压缩与标题生成该走 fast 层：用主模型做系统开销等于每次都付高价推理的钱
	got, ok := r.ModelFor(modelrouter.TierFast)
	if !ok || got != fast {
		t.Fatalf("ModelFor(fast) = %v, %v", got, ok)
	}
	if _, ok := r.ModelFor(modelrouter.TierVision); ok {
		t.Error("ModelFor returned a model for an unconfigured tier")
	}
}

func TestSample_SelectsNamedModel(t *testing.T) {
	t.Parallel()

	selected := faux.New(faux.Text("selected"))
	standard := faux.New(faux.Text("standard"))
	r := mustRouter(t, modelrouter.Config{
		Models:      map[modelrouter.Tier]model.Model{modelrouter.TierStandard: standard},
		NamedModels: map[string]model.Model{"chosen": selected},
	})
	st := newState(user("hi"))
	st.Values = map[string]any{"model_name": "chosen"}

	resp, _, err := r.Sample(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Message.Content; got != "selected" {
		t.Fatalf("content=%q, want selected", got)
	}
	if standard.CallCount() != 0 {
		t.Fatal("standard model was called for an explicit named selection")
	}
}

func TestSample_RejectsUnknownNamedModel(t *testing.T) {
	t.Parallel()

	r := mustRouter(t, modelrouter.Config{
		Models:      map[modelrouter.Tier]model.Model{modelrouter.TierStandard: faux.New(faux.Text("standard"))},
		NamedModels: map[string]model.Model{"known": faux.New(faux.Text("known"))},
	})
	st := newState(user("hi"))
	st.Values = map[string]any{"model_name": "missing"}

	_, _, err := r.Sample(context.Background(), st)
	if err == nil || !strings.Contains(err.Error(), `unknown model "missing"; available: [known]`) {
		t.Fatalf("error=%v", err)
	}
}

func TestSample_ConcurrentIsSafe(t *testing.T) {
	t.Parallel()

	turns := make([]faux.Turn, 50)
	for i := range turns {
		turns[i] = faux.Text("ok")
	}

	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{
		modelrouter.TierStandard: faux.New(turns...),
	}})

	var failures atomic.Int64
	done := make(chan struct{})
	for range 40 {
		go func() {
			defer func() { done <- struct{}{} }()
			if _, _, err := r.Sample(context.Background(), newState(user("hi"))); err != nil {
				failures.Add(1)
			}
		}()
	}
	for range 40 {
		<-done
	}
	if failures.Load() != 0 {
		t.Fatalf("%d concurrent samples failed", failures.Load())
	}
}

// --- helpers ---

type fakeCompactor struct {
	compacted   bool
	err         error
	calls       int
	replaceWith []message.Message
}

func (f *fakeCompactor) MaybeCompact(_ context.Context, h *message.History) (bool, error) {
	f.calls++
	if f.err != nil {
		return false, f.err
	}
	if f.compacted && f.replaceWith != nil && h != nil {
		h.Replace(f.replaceWith)
	}
	return f.compacted, nil
}

// streamFailAfterDeltas 先流出内容再在 Result 处失败，用于测试"已流出不重试"。
type streamFailAfterDeltas struct{ inner model.Model }

func (s streamFailAfterDeltas) Info() model.Info { return s.inner.Info() }

func (s streamFailAfterDeltas) Complete(ctx context.Context, req model.Request) (*model.Response, error) {
	return s.inner.Complete(ctx, req)
}

func (s streamFailAfterDeltas) Stream(ctx context.Context, req model.Request) (model.StreamReader, error) {
	r, err := s.inner.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	return failingReader{inner: r}, nil
}

type failingReader struct{ inner model.StreamReader }

func (f failingReader) Next() (model.StreamEvent, bool) { return f.inner.Next() }
func (f failingReader) Close() error                    { return f.inner.Close() }
func (f failingReader) Result() (*model.Response, error) {
	return nil, errors.New("connection reset mid-stream")
}
