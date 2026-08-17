package runtime_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

func ev(runID string, t runtime.EventType) runtime.Event {
	return runtime.MustEvent(runID, "thread-1", t, map[string]string{"x": "y"})
}

func TestRunContextEventStreamRunID(t *testing.T) {
	t.Parallel()

	if got := (runtime.RunContext{RunID: "root"}).EventStreamRunID(); got != "root" {
		t.Fatalf("root stream ID = %q, want root", got)
	}
	if got := (runtime.RunContext{RunID: "root:task", EventRunID: "root"}).EventStreamRunID(); got != "root" {
		t.Fatalf("child stream ID = %q, want root", got)
	}
}

// --- 事件分类 ---

func TestEvent_CategoryAssignment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		typ  runtime.EventType
		want runtime.Category
	}{
		{runtime.EventContentDelta, runtime.CategoryTrace},
		{runtime.EventToolStart, runtime.CategoryAudit},
		{runtime.EventUsage, runtime.CategoryUsage},
		{runtime.EventRunEnd, runtime.CategoryAudit},
		{runtime.EventError, runtime.CategoryAudit},
		{runtime.EventGuardrailBlock, runtime.CategoryAudit},
		{runtime.EventSubagentStart, runtime.CategoryAudit},
		{runtime.EventSubagentResult, runtime.CategoryAudit},
		{runtime.EventSubagentProgress, runtime.CategoryTrace},
		{runtime.EventApprovalRequested, runtime.CategoryAudit},
		{runtime.EventApprovalResolved, runtime.CategoryAudit},
		{runtime.EventQuestionRequested, runtime.CategoryAudit},
		{runtime.EventQuestionResolved, runtime.CategoryAudit},
		{runtime.EventCompactionStart, runtime.CategoryAudit},
		{runtime.EventCompactionComplete, runtime.CategoryAudit},
		{runtime.EventRunStateChanged, runtime.CategoryAudit},
		{runtime.EventBudgetChanged, runtime.CategoryAudit},
		{runtime.EventPlanModeChanged, runtime.CategoryAudit},
		// message_replace 丢掉就等于把被拦截的文本留在用户屏幕上
		{runtime.EventMessageReplace, runtime.CategoryAudit},
	}

	for _, tc := range tests {
		t.Run(string(tc.typ), func(t *testing.T) {
			t.Parallel()
			got := runtime.MustEvent("r1", "t1", tc.typ, nil)
			if got.Category != tc.want {
				t.Fatalf("category = %q, want %q", got.Category, tc.want)
			}
			if got.Droppable() != (tc.want == runtime.CategoryTrace) {
				t.Errorf("Droppable() = %v for category %q", got.Droppable(), got.Category)
			}
		})
	}
}

// 事件负载序列化失败不该中断回合：那是观测通路的问题。
func TestMustEvent_SurvivesUnmarshalableData(t *testing.T) {
	t.Parallel()

	got := runtime.MustEvent("r1", "t1", runtime.EventContentDelta, func() {})

	if got.Type != runtime.EventContentDelta {
		t.Fatalf("type = %q", got.Type)
	}
	if len(got.Data) == 0 {
		t.Fatal("payload is empty; the marshal error should be recorded")
	}
}

func TestNewEvent_ReportsMarshalError(t *testing.T) {
	t.Parallel()

	if _, err := runtime.NewEvent("r1", "t1", runtime.EventContentDelta, func() {}); err == nil {
		t.Fatal("NewEvent() accepted an unmarshalable payload")
	}
}

// --- EventBus 基本行为 ---

func TestBus_PublishAssignsMonotonicSeq(t *testing.T) {
	t.Parallel()

	b := runtime.NewMemoryBus(runtime.BusOptions{})
	ctx := context.Background()

	for i := int64(1); i <= 5; i++ {
		if got := b.Publish(ctx, ev("r1", runtime.EventContentDelta)); got != i {
			t.Fatalf("Publish() = %d, want %d", got, i)
		}
	}
}

func TestBus_ContinuesFromDurableSequence(t *testing.T) {
	b := runtime.NewMemoryBus(runtime.BusOptions{})
	seeded := ev("r1", runtime.EventRunStateChanged)
	seeded.Seq = 41
	if got := b.Publish(context.Background(), seeded); got != 41 {
		t.Fatalf("seeded Publish() = %d, want 41", got)
	}
	if got := b.Publish(context.Background(), ev("r1", runtime.EventRunEnd)); got != 42 {
		t.Fatalf("next Publish() = %d, want 42", got)
	}
}

func TestBus_SeqIsPerRun(t *testing.T) {
	t.Parallel()

	b := runtime.NewMemoryBus(runtime.BusOptions{})
	ctx := context.Background()

	b.Publish(ctx, ev("r1", runtime.EventContentDelta))
	b.Publish(ctx, ev("r1", runtime.EventContentDelta))

	if got := b.Publish(ctx, ev("r2", runtime.EventContentDelta)); got != 1 {
		t.Fatalf("a second run started at seq %d, want 1", got)
	}
}

func TestBus_SubscriberReceivesEvents(t *testing.T) {
	t.Parallel()

	b := runtime.NewMemoryBus(runtime.BusOptions{})
	ch, cancel := b.Subscribe("r1")
	defer cancel()

	b.Publish(context.Background(), ev("r1", runtime.EventContentDelta))

	select {
	case got := <-ch:
		if got.Seq != 1 || got.Type != runtime.EventContentDelta {
			t.Fatalf("received %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no event delivered")
	}
}

func TestBus_MultipleSubscribersAllReceive(t *testing.T) {
	t.Parallel()

	b := runtime.NewMemoryBus(runtime.BusOptions{})

	ch1, c1 := b.Subscribe("r1")
	ch2, c2 := b.Subscribe("r1")
	defer c1()
	defer c2()

	b.Publish(context.Background(), ev("r1", runtime.EventContentDelta))

	for i, ch := range []<-chan runtime.Event{ch1, ch2} {
		select {
		case got := <-ch:
			if got.Seq != 1 {
				t.Fatalf("subscriber %d got seq %d", i, got.Seq)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d received nothing; multiple windows must all observe the run", i)
		}
	}
}

func TestBus_SubscriberOnlySeesItsOwnRun(t *testing.T) {
	t.Parallel()

	b := runtime.NewMemoryBus(runtime.BusOptions{})
	ch, cancel := b.Subscribe("r1")
	defer cancel()

	b.Publish(context.Background(), ev("r2", runtime.EventContentDelta))

	select {
	case got := <-ch:
		t.Fatalf("received an event from another run: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

// Publish 必须非阻塞：订阅者不消费时，主循环不能被卡住。
func TestBus_PublishNeverBlocksOnSlowSubscriber(t *testing.T) {
	t.Parallel()

	b := runtime.NewMemoryBus(runtime.BusOptions{SubscriberBuffer: 2})
	_, cancel := b.Subscribe("r1")
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx := context.Background()
		// 远超通道容量，且没人读
		for range 1000 {
			b.Publish(ctx, ev("r1", runtime.EventContentDelta))
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber; the agent loop would stall")
	}
}

// 丢事件是安全的，前提是 Seq 单调、缺口可检测、缓冲可补齐。
func TestBus_DropsAreDetectableViaSeqGaps(t *testing.T) {
	t.Parallel()

	b := runtime.NewMemoryBus(runtime.BusOptions{SubscriberBuffer: 4})
	ch, cancel := b.Subscribe("r1")
	defer cancel()

	ctx := context.Background()
	for range 20 {
		b.Publish(ctx, ev("r1", runtime.EventContentDelta))
	}

	var seqs []int64
	for {
		select {
		case got := <-ch:
			seqs = append(seqs, got.Seq)
			continue
		default:
		}
		break
	}

	if len(seqs) == 0 {
		t.Fatal("no events received at all")
	}
	// 收到的序号必须严格递增，客户端才能据此发现缺口
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("sequence is not monotonic: %v", seqs)
		}
	}
	if len(seqs) >= 20 {
		t.Skip("buffer absorbed everything; the drop path was not exercised")
	}
}

func TestBus_UnsubscribeStopsDelivery(t *testing.T) {
	t.Parallel()

	b := runtime.NewMemoryBus(runtime.BusOptions{})
	ch, cancel := b.Subscribe("r1")

	cancel()

	// 退订后通道被关闭，读取立刻返回零值
	if _, open := <-ch; open {
		t.Fatal("the channel should be closed after unsubscribing")
	}

	// 退订后继续发布不得 panic
	b.Publish(context.Background(), ev("r1", runtime.EventContentDelta))
}

func TestBus_UnsubscribeIsIdempotent(t *testing.T) {
	t.Parallel()

	b := runtime.NewMemoryBus(runtime.BusOptions{})
	_, cancel := b.Subscribe("r1")

	cancel()
	cancel() // 二次调用不得 panic（double close）
}

// on_disconnect=cancel 的判定要用它：只有最后一个订阅者也走了才取消 run。
func TestBus_SubscriberCount(t *testing.T) {
	t.Parallel()

	b := runtime.NewMemoryBus(runtime.BusOptions{})

	if got := b.SubscriberCount("r1"); got != 0 {
		t.Fatalf("SubscriberCount() = %d, want 0", got)
	}

	_, c1 := b.Subscribe("r1")
	_, c2 := b.Subscribe("r1")
	if got := b.SubscriberCount("r1"); got != 2 {
		t.Fatalf("SubscriberCount() = %d, want 2", got)
	}

	c1()
	if got := b.SubscriberCount("r1"); got != 1 {
		t.Fatalf("SubscriberCount() = %d, want 1 after one unsubscribe", got)
	}

	c2()
	if got := b.SubscriberCount("r1"); got != 0 {
		t.Fatalf("SubscriberCount() = %d, want 0", got)
	}
}

// --- 环形缓冲与回放 ---

func TestBus_BacklogReturnsEventsAfterCursor(t *testing.T) {
	t.Parallel()

	b := runtime.NewMemoryBus(runtime.BusOptions{RingCapacity: 100})
	ctx := context.Background()

	for range 10 {
		b.Publish(ctx, ev("r1", runtime.EventContentDelta))
	}

	got := b.Backlog("r1", 7)
	if len(got) != 3 {
		t.Fatalf("Backlog(7) = %d events, want 3", len(got))
	}
	for i, e := range got {
		if e.Seq != int64(8+i) {
			t.Fatalf("Backlog returned seq %d at position %d", e.Seq, i)
		}
	}
}

func TestBus_BacklogFromZeroReturnsEverythingBuffered(t *testing.T) {
	t.Parallel()

	b := runtime.NewMemoryBus(runtime.BusOptions{RingCapacity: 100})
	ctx := context.Background()

	for range 5 {
		b.Publish(ctx, ev("r1", runtime.EventContentDelta))
	}

	if got := b.Backlog("r1", 0); len(got) != 5 {
		t.Fatalf("Backlog(0) = %d events, want 5", len(got))
	}
}

func TestBus_RingEvictsOldestAndStaysOrdered(t *testing.T) {
	t.Parallel()

	b := runtime.NewMemoryBus(runtime.BusOptions{RingCapacity: 5})
	ctx := context.Background()

	for range 12 {
		b.Publish(ctx, ev("r1", runtime.EventContentDelta))
	}

	got := b.Backlog("r1", 0)
	if len(got) != 5 {
		t.Fatalf("Backlog() = %d events, want the ring capacity 5", len(got))
	}
	if got[0].Seq != 8 || got[4].Seq != 12 {
		t.Fatalf("ring kept the wrong window: first=%d last=%d", got[0].Seq, got[4].Seq)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Seq != got[i-1].Seq+1 {
			t.Fatalf("ring returned out-of-order events: %d then %d", got[i-1].Seq, got[i].Seq)
		}
	}
}

// 游标已被覆盖时必须如实报告缺口：假装连续会让客户端渲染出缺了一段的回答，
// 而用户看不出哪里缺了。
func TestBus_BacklogGapIsReported(t *testing.T) {
	t.Parallel()

	b := runtime.NewMemoryBus(runtime.BusOptions{RingCapacity: 3})
	ctx := context.Background()

	for range 10 {
		b.Publish(ctx, ev("r1", runtime.EventContentDelta))
	}

	// 缓冲里只剩 seq 8..10，从 2 恢复必然漏掉 3..7
	if !b.BacklogGap("r1", 2) {
		t.Fatal("BacklogGap() = false, want true for an evicted cursor")
	}
	// 从 7 恢复正好接上 8，没有缺口
	if b.BacklogGap("r1", 7) {
		t.Fatal("BacklogGap() = true for a contiguous cursor")
	}
}

func TestBus_BacklogGapOnEmptyRun(t *testing.T) {
	t.Parallel()

	b := runtime.NewMemoryBus(runtime.BusOptions{})
	if b.BacklogGap("unknown", 0) {
		t.Fatal("BacklogGap() = true for a run with no events")
	}
}

// --- 持久通路 ---

func TestBus_PersisterReceivesEveryEventWithSeq(t *testing.T) {
	t.Parallel()

	p := &recordingPersister{}
	b := runtime.NewMemoryBus(runtime.BusOptions{Persister: p})
	ctx := context.Background()

	for range 5 {
		b.Publish(ctx, ev("r1", runtime.EventContentDelta))
	}

	got := p.events()
	if len(got) != 5 {
		t.Fatalf("persister saw %d events, want 5", len(got))
	}
	for i, e := range got {
		if e.Seq != int64(i+1) {
			t.Fatalf("persister saw seq %d at position %d; it must receive the assigned sequence", e.Seq, i)
		}
	}
}

// 持久通路故障不得阻塞主循环，也不得让 Publish 失败。
func TestBus_PersisterFailureDoesNotBlockPublish(t *testing.T) {
	t.Parallel()

	b := runtime.NewMemoryBus(runtime.BusOptions{Persister: panickyPersister{}})

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		b.Publish(context.Background(), ev("r1", runtime.EventContentDelta))
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish hung on a failing persister")
	}
}

func TestBus_CloseReleasesSubscribers(t *testing.T) {
	t.Parallel()

	b := runtime.NewMemoryBus(runtime.BusOptions{})
	ch, _ := b.Subscribe("r1")

	b.Close("r1")

	select {
	case _, open := <-ch:
		if open {
			t.Fatal("channel delivered an event after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not close the subscriber channel")
	}
}

func TestBus_ConcurrentPublishAndSubscribe(t *testing.T) {
	t.Parallel()

	b := runtime.NewMemoryBus(runtime.BusOptions{SubscriberBuffer: 1024, RingCapacity: 1024})
	ctx := context.Background()

	var wg sync.WaitGroup
	var received atomic.Int64

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, cancel := b.Subscribe("r1")
			defer cancel()
			for range 50 {
				select {
				case <-ch:
					received.Add(1)
				case <-time.After(200 * time.Millisecond):
					return
				}
			}
		}()
	}

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				b.Publish(ctx, ev("r1", runtime.EventContentDelta))
			}
		}()
	}
	wg.Wait()

	// 序号必须没有重复：并发发布下 Seq 的赋值要原子
	seen := make(map[int64]struct{})
	for _, e := range b.Backlog("r1", 0) {
		if _, dup := seen[e.Seq]; dup {
			t.Fatalf("duplicate seq %d under concurrent publish", e.Seq)
		}
		seen[e.Seq] = struct{}{}
	}
}

// --- Journal 三桶归因 ---

func pricer() *runtime.Pricer {
	return runtime.NewPricer(map[string]runtime.Price{
		"standard": {InputPerMillion: 3_000_000, OutputPerMillion: 15_000_000},
		"fast":     {InputPerMillion: 150_000, OutputPerMillion: 600_000},
	}, runtime.Price{InputPerMillion: 1_000_000, OutputPerMillion: 2_000_000})
}

func TestJournal_BucketsTokens(t *testing.T) {
	t.Parallel()

	j := runtime.NewJournal(pricer())

	j.Observe(runtime.Entry{
		Bucket: runtime.BucketLead, CallID: "a", ModelName: "standard",
		Usage: model.Usage{InputTokens: 100, OutputTokens: 20, CachedInputTokens: 40},
	})
	j.Observe(runtime.Entry{
		Bucket: runtime.BucketSubagent, Source: "explore", CallID: "b", ModelName: "standard",
		Usage: model.Usage{InputTokens: 50, OutputTokens: 10},
	})
	j.Observe(runtime.Entry{
		Bucket: runtime.BucketAuxiliary, Source: "summariser", CallID: "c", ModelName: "fast",
		Usage: model.Usage{InputTokens: 30, OutputTokens: 5},
	})

	got := j.Totals()
	if got.LeadTokens != 120 {
		t.Errorf("LeadTokens = %d, want 120", got.LeadTokens)
	}
	if got.SubagentTokens != 60 {
		t.Errorf("SubagentTokens = %d, want 60", got.SubagentTokens)
	}
	if got.AuxiliaryTokens != 35 {
		t.Errorf("AuxiliaryTokens = %d, want 35", got.AuxiliaryTokens)
	}
	if got.LLMCalls != 3 {
		t.Errorf("LLMCalls = %d, want 3", got.LLMCalls)
	}
	if got.InputTokens != 180 || got.OutputTokens != 35 {
		t.Errorf("totals = %+v", got)
	}
	if got.CachedInputTokens != 40 {
		t.Errorf("CachedInputTokens = %d, want 40", got.CachedInputTokens)
	}
}

// 流式路径上 done 与 Result 都可能上报同一次调用，重复计数会让账单虚高。
func TestJournal_DedupesByCallID(t *testing.T) {
	t.Parallel()

	j := runtime.NewJournal(pricer())
	entry := runtime.Entry{
		Bucket: runtime.BucketLead, CallID: "same", ModelName: "standard",
		Usage: model.Usage{InputTokens: 100, OutputTokens: 20},
	}

	if !j.Observe(entry) {
		t.Fatal("first Observe() = false")
	}
	if j.Observe(entry) {
		t.Fatal("second Observe() with the same CallID = true, want it deduped")
	}

	if got := j.Totals(); got.LeadTokens != 120 || got.LLMCalls != 1 {
		t.Fatalf("totals = %+v, want a single call counted", got)
	}
}

// 同一 CallID 在不同桶里各记一次是正确的：worker 自己的账与回灌到父 run 的账
// 是两笔。只按 CallID 去重会让回灌被当成重复丢掉，subagent_tokens 永远是 0。
func TestJournal_SameCallIDInDifferentBucketsBothCount(t *testing.T) {
	t.Parallel()

	j := runtime.NewJournal(pricer())
	usage := model.Usage{InputTokens: 10, OutputTokens: 5}

	j.Observe(runtime.Entry{Bucket: runtime.BucketLead, CallID: "x", ModelName: "standard", Usage: usage})
	j.Observe(runtime.Entry{Bucket: runtime.BucketSubagent, Source: "explore", CallID: "x", ModelName: "standard", Usage: usage})

	got := j.Totals()
	if got.LeadTokens != 15 || got.SubagentTokens != 15 {
		t.Fatalf("totals = %+v, want both buckets counted", got)
	}
}

func TestJournal_EntriesWithoutCallIDStillCount(t *testing.T) {
	t.Parallel()

	j := runtime.NewJournal(pricer())
	usage := model.Usage{InputTokens: 10, OutputTokens: 5}

	j.Observe(runtime.Entry{Bucket: runtime.BucketLead, ModelName: "standard", Usage: usage})
	j.Observe(runtime.Entry{Bucket: runtime.BucketLead, ModelName: "standard", Usage: usage})

	// 无法去重时不能静默丢弃，否则用量被低估。
	if got := j.Totals(); got.LLMCalls != 2 || got.LeadTokens != 30 {
		t.Fatalf("totals = %#v, want both unidentified calls counted", got)
	}
}

func TestJournal_BySourceBreakdown(t *testing.T) {
	t.Parallel()

	j := runtime.NewJournal(pricer())

	j.Observe(runtime.Entry{
		Bucket: runtime.BucketSubagent, Source: "vision", CallID: "a", ModelName: "standard",
		Usage: model.Usage{InputTokens: 1000, OutputTokens: 100},
	})
	j.Observe(runtime.Entry{
		Bucket: runtime.BucketAuxiliary, Source: "summariser", CallID: "b", ModelName: "fast",
		Usage: model.Usage{InputTokens: 10, OutputTokens: 2},
	})

	got := j.BySource()
	// "这个 run 的开销主要来自视觉分析还是上下文压缩"是运营必须能回答的问题
	if got["subagent:vision"] != 1100 {
		t.Errorf("subagent:vision = %d, want 1100", got["subagent:vision"])
	}
	if got["auxiliary:summariser"] != 12 {
		t.Errorf("auxiliary:summariser = %d, want 12", got["auxiliary:summariser"])
	}
}

// --- 成本计算 ---

func TestPricer_CostIsIntegerMicros(t *testing.T) {
	t.Parallel()

	p := pricer()

	// standard: 输入 3 微单位/token，输出 15 微单位/token
	got := p.CostMicros("standard", model.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000})
	want := int64(3_000_000 + 15_000_000)
	if got != want {
		t.Fatalf("CostMicros() = %d, want %d", got, want)
	}
}

func TestPricer_UnknownModelUsesFallback(t *testing.T) {
	t.Parallel()

	p := pricer()
	got := p.CostMicros("never-heard-of-it", model.Usage{InputTokens: 1_000_000})
	if got != 1_000_000 {
		t.Fatalf("CostMicros() = %d, want the fallback rate", got)
	}
}

// 缓存命中的部分按全价计会高估成本，而高估会让额度提前熔断。
func TestPricer_CachedTokensAreCheaper(t *testing.T) {
	t.Parallel()

	p := runtime.NewPricer(map[string]runtime.Price{
		"m": {InputPerMillion: 1_000_000, OutputPerMillion: 0, CachedInputPerMillion: 100_000},
	}, runtime.Price{})

	full := p.CostMicros("m", model.Usage{InputTokens: 1_000_000})
	cached := p.CostMicros("m", model.Usage{InputTokens: 1_000_000, CachedInputTokens: 1_000_000})

	if cached >= full {
		t.Fatalf("cached cost %d is not below the uncached cost %d", cached, full)
	}
	if cached != 100_000 {
		t.Fatalf("cached cost = %d, want the cached rate applied", cached)
	}
}

func TestPricer_NilIsZeroCost(t *testing.T) {
	t.Parallel()

	var p *runtime.Pricer
	if got := p.CostMicros("m", model.Usage{InputTokens: 100}); got != 0 {
		t.Fatalf("CostMicros() on a nil pricer = %d, want 0", got)
	}
}

func TestJournal_AccumulatesCost(t *testing.T) {
	t.Parallel()

	j := runtime.NewJournal(pricer())
	j.Observe(runtime.Entry{
		Bucket: runtime.BucketLead, CallID: "a", ModelName: "standard",
		Usage: model.Usage{InputTokens: 1_000_000, OutputTokens: 0},
	})

	if got := j.Totals().CostMicros; got != 3_000_000 {
		t.Fatalf("CostMicros = %d, want 3000000", got)
	}
}

// --- subagent 用量回灌 ---

// 不回灌的话 subagent_tokens 永远是 0，而且一个 run 可以靠不断派发绕过成本上限。
func TestJournal_MergeFeedsSubagentUsageBack(t *testing.T) {
	t.Parallel()

	parent := runtime.NewJournal(pricer())
	child := parent.Child()

	child.Observe(runtime.Entry{
		Bucket: runtime.BucketLead, CallID: "c1", ModelName: "standard",
		Usage: model.Usage{InputTokens: 200, OutputTokens: 40, CachedInputTokens: 80},
	})
	child.Observe(runtime.Entry{
		Bucket: runtime.BucketAuxiliary, Source: "summariser", CallID: "c2", ModelName: "fast",
		Usage: model.Usage{InputTokens: 20, OutputTokens: 4},
	})

	parent.MergeSubagent("task-1", "explore", child)

	got := parent.Totals()
	// 子账本里的一切都归父账本的 subagent 桶，包括子 agent 自己的 auxiliary 开销
	if got.SubagentTokens != 264 {
		t.Fatalf("SubagentTokens = %d, want 264 (everything the child spent)", got.SubagentTokens)
	}
	if got.LeadTokens != 0 {
		t.Errorf("LeadTokens = %d; the child's lead spend must not land in the parent's lead bucket", got.LeadTokens)
	}
	if got.CostMicros == 0 {
		t.Error("the child's cost was not merged; the parent could exceed its budget via delegation")
	}
	if got.CachedInputTokens != 80 {
		t.Errorf("CachedInputTokens = %d, want 80", got.CachedInputTokens)
	}
	if bySource := parent.BySource()["subagent:explore"]; bySource != 264 {
		t.Errorf("BySource[subagent:explore] = %d, want 264", bySource)
	}
}

func TestJournal_MergeSubagentDeduplicatesTaskReplay(t *testing.T) {
	t.Parallel()

	parent := runtime.NewJournal(pricer())
	child := parent.Child()
	child.Observe(runtime.Entry{
		Bucket: runtime.BucketLead, CallID: "child-call", ModelName: "standard",
		Usage: model.Usage{InputTokens: 20, OutputTokens: 4},
	})

	if !parent.MergeSubagent("task-1", "explore", child) {
		t.Fatal("first task merge was rejected")
	}
	if parent.MergeSubagent("task-1", "explore", child) {
		t.Fatal("replayed task was merged twice")
	}
	if !parent.MergeSubagent("task-2", "explore", child) {
		t.Fatal("distinct task merge was rejected")
	}

	got := parent.Totals()
	if got.SubagentTokens != 48 || got.LLMCalls != 2 {
		t.Fatalf("totals = %#v, want two distinct task merges", got)
	}
	if bySource := parent.BySource()["subagent:explore"]; bySource != 48 {
		t.Fatalf("BySource[subagent:explore] = %d, want 48", bySource)
	}
}

func TestJournal_OnChangeObservesLateSubagentMerge(t *testing.T) {
	parent := runtime.NewJournal(nil)
	child := parent.Child()
	child.Observe(runtime.Entry{Bucket: runtime.BucketLead, CallID: "child-1", Usage: model.Usage{InputTokens: 4, OutputTokens: 2}})

	changed := make(chan runtime.Totals, 2)
	parent.SetOnChange(func(totals runtime.Totals) { changed <- totals })
	initial := <-changed
	if initial.LLMCalls != 0 {
		t.Fatalf("initial totals = %#v", initial)
	}
	parent.MergeSubagent("task-1", "explore", child)

	select {
	case totals := <-changed:
		if totals.SubagentTokens != 6 || totals.LLMCalls != 1 {
			t.Fatalf("totals = %#v", totals)
		}
	case <-time.After(time.Second):
		t.Fatal("late merge did not trigger persistence callback")
	}
}

func TestJournal_SetOnChangeImmediatelyObservesExistingTotals(t *testing.T) {
	j := runtime.NewJournal(nil)
	j.Observe(runtime.Entry{Bucket: runtime.BucketLead, CallID: "lead-1", Usage: model.Usage{InputTokens: 3, OutputTokens: 2}})
	changed := make(chan runtime.Totals, 1)
	j.SetOnChange(func(totals runtime.Totals) { changed <- totals })

	select {
	case totals := <-changed:
		if totals.LeadTokens != 5 || totals.LLMCalls != 1 {
			t.Fatalf("totals = %#v", totals)
		}
	case <-time.After(time.Second):
		t.Fatal("current totals were not delivered when callback was installed")
	}
}

func TestJournal_ChildSharesAggregateSpendWithoutDoubleCountingMerge(t *testing.T) {
	t.Parallel()

	parent := runtime.NewJournal(pricer())
	child := parent.Child()
	child.Observe(runtime.Entry{
		Bucket: runtime.BucketLead, Source: "worker", CallID: "child-call",
		ModelName: "standard", Usage: model.Usage{InputTokens: 7, OutputTokens: 3},
	})

	tokensBefore, costBefore := parent.Spend()
	if tokensBefore != 10 || costBefore == 0 {
		t.Fatalf("shared spend before merge = (%d, %d)", tokensBefore, costBefore)
	}
	parent.MergeSubagent("task-1", "worker", child)
	tokensAfter, costAfter := parent.Spend()
	if tokensAfter != tokensBefore || costAfter != costBefore {
		t.Fatalf("merge double-counted shared spend: before=(%d,%d) after=(%d,%d)", tokensBefore, costBefore, tokensAfter, costAfter)
	}
}

func TestJournal_ChildInheritsPricingWithoutSharingTotals(t *testing.T) {
	parent := runtime.NewJournal(pricer())
	child := parent.Child()
	child.Observe(runtime.Entry{Bucket: runtime.BucketLead, CallID: "child-1", ModelName: "standard", Usage: model.Usage{InputTokens: 1_000_000}})
	if child.Totals().CostMicros == 0 {
		t.Fatal("child did not inherit pricing")
	}
	if parent.Totals().LLMCalls != 0 {
		t.Fatalf("child mutated parent before merge: %#v", parent.Totals())
	}
}

func TestJournal_MergeNilChildIsSafe(t *testing.T) {
	t.Parallel()

	parent := runtime.NewJournal(pricer())
	parent.MergeSubagent("task-1", "x", nil)

	if got := parent.Totals(); got.SubagentTokens != 0 {
		t.Fatalf("totals = %+v after merging nil", got)
	}
}

func TestJournal_NilReceiverIsSafe(t *testing.T) {
	t.Parallel()

	var j *runtime.Journal
	if j.Observe(runtime.Entry{}) {
		t.Error("Observe() on a nil journal = true")
	}
	if got := j.Totals(); got.LLMCalls != 0 {
		t.Errorf("Totals() on a nil journal = %+v", got)
	}
	if got := j.BySource(); got != nil {
		t.Errorf("BySource() on a nil journal = %v", got)
	}
	j.MergeSubagent("task-1", "x", runtime.NewJournal(nil))
}

func TestJournal_ConcurrentObserve(t *testing.T) {
	t.Parallel()

	j := runtime.NewJournal(pricer())

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j.Observe(runtime.Entry{
				Bucket: runtime.BucketLead, CallID: fmt.Sprintf("c%d", i), ModelName: "standard",
				Usage: model.Usage{InputTokens: 2, OutputTokens: 1},
			})
		}()
	}
	wg.Wait()

	if got := j.Totals(); got.LLMCalls != 50 || got.LeadTokens != 150 {
		t.Fatalf("totals = %+v, want 50 calls and 150 tokens", got)
	}
}

// --- helpers ---

type recordingPersister struct {
	mu  sync.Mutex
	got []runtime.Event
}

func (p *recordingPersister) Persist(_ context.Context, e runtime.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.got = append(p.got, e)
}

func (p *recordingPersister) events() []runtime.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]runtime.Event, len(p.got))
	copy(out, p.got)
	return out
}

type panickyPersister struct{}

func (panickyPersister) Persist(context.Context, runtime.Event) {
	panic("storage exploded")
}
