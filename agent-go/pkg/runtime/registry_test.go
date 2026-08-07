package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

func newRegistry(t *testing.T, backend runtime.RegistryBackend, ttl time.Duration) *runtime.Registry {
	t.Helper()

	r, err := runtime.NewRegistry(context.Background(), runtime.RegistryOptions{
		Backend: backend,
		Owner:   "instance-a",
		TTL:     ttl,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	t.Cleanup(r.Close)
	return r
}

func rec(runID string, policy runtime.DisconnectPolicy) runtime.RunRecord {
	return runtime.RunRecord{RunID: runID, ThreadID: "t1", OnDisconnect: policy}
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	t.Parallel()

	r := newRegistry(t, nil, time.Minute)
	ctx := context.Background()

	if _, err := r.Register(ctx, rec("run-1", runtime.DisconnectCancel)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got, err := r.Get(ctx, "run-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Status != runtime.StatusRunning {
		t.Errorf("Status = %q, want running", got.Status)
	}
	if got.Owner != "instance-a" {
		t.Errorf("Owner = %q; it must record which instance holds the run", got.Owner)
	}
	if got.StartedAt.IsZero() {
		t.Error("StartedAt was not set")
	}
}

func TestRegistry_RejectsEmptyRunID(t *testing.T) {
	t.Parallel()

	r := newRegistry(t, nil, time.Minute)
	if _, err := r.Register(context.Background(), rec("", runtime.DisconnectCancel)); err == nil {
		t.Fatal("Register() accepted an empty run id")
	}
}

func TestRegistry_DefaultsInvalidPolicyToCancel(t *testing.T) {
	t.Parallel()

	r := newRegistry(t, nil, time.Minute)
	ctx := context.Background()

	if _, err := r.Register(ctx, rec("run-1", "whatever")); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Get(ctx, "run-1")
	if got.OnDisconnect != runtime.DisconnectCancel {
		t.Fatalf("OnDisconnect = %q, want cancel as the safe default", got.OnDisconnect)
	}
}

func TestRegistry_UnknownRunIsNotFound(t *testing.T) {
	t.Parallel()

	r := newRegistry(t, nil, time.Minute)
	if _, err := r.Get(context.Background(), "nope"); !errors.Is(err, runtime.ErrRunNotFound) {
		t.Fatalf("Get() error = %v, want ErrRunNotFound", err)
	}
}

// run 的生命周期必须脱离发起它的 HTTP 请求，on_disconnect=continue 才成立。
func TestRegistry_RunContextOutlivesTheRequestContext(t *testing.T) {
	t.Parallel()

	r := newRegistry(t, nil, time.Minute)

	reqCtx, cancelReq := context.WithCancel(context.Background())
	runCtx, err := r.Register(reqCtx, rec("run-1", runtime.DisconnectContinue))
	if err != nil {
		t.Fatal(err)
	}

	cancelReq() // 客户端断开

	select {
	case <-runCtx.Done():
		t.Fatal("the run context was cancelled with the request; on_disconnect=continue would be impossible")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRegistry_CancelStopsTheRunContext(t *testing.T) {
	t.Parallel()

	r := newRegistry(t, nil, time.Minute)
	ctx := context.Background()

	runCtx, err := r.Register(ctx, rec("run-1", runtime.DisconnectContinue))
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Cancel(ctx, "run-1"); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}

	select {
	case <-runCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("Cancel() did not stop the run context")
	}
}

func TestRegistry_CancelUnknownRunIsNotFound(t *testing.T) {
	t.Parallel()

	r := newRegistry(t, nil, time.Minute)
	if err := r.Cancel(context.Background(), "nope"); !errors.Is(err, runtime.ErrRunNotFound) {
		t.Fatalf("Cancel() error = %v, want ErrRunNotFound", err)
	}
}

func TestRegistry_CancelCompletedRunIsNoop(t *testing.T) {
	t.Parallel()

	r := newRegistry(t, nil, time.Minute)
	ctx := context.Background()

	if _, err := r.Register(ctx, rec("run-1", runtime.DisconnectCancel)); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(ctx, "run-1", runtime.StatusCompleted); err != nil {
		t.Fatal(err)
	}

	if err := r.Cancel(ctx, "run-1"); err != nil {
		t.Fatalf("Cancel() on a finished run error = %v, want nil", err)
	}
}

// 取消必须跨实例生效：发给实例 A 的取消命令要能停掉实例 B 持有的 run。
// nous-agent 的进程内 task_registry 在这里会失效，所以本设计不采纳它。
func TestRegistry_CancelCrossesInstances(t *testing.T) {
	t.Parallel()

	// 两个 Registry 共享同一个后端，模拟两个进程
	backend := runtime.NewMemoryRegistryBackend()
	ctx := context.Background()

	holder, err := runtime.NewRegistry(ctx, runtime.RegistryOptions{
		Backend: backend, Owner: "instance-holder", TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()

	other, err := runtime.NewRegistry(ctx, runtime.RegistryOptions{
		Backend: backend, Owner: "instance-other", TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	runCtx, err := holder.Register(ctx, rec("run-1", runtime.DisconnectContinue))
	if err != nil {
		t.Fatal(err)
	}

	// 取消命令发给不持有该 run 的实例
	if err := other.Cancel(ctx, "run-1"); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}

	select {
	case <-runCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("a cancel sent to another instance never reached the holder")
	}
}

func TestRegistry_CompleteMarksStatusAndClearsLocalState(t *testing.T) {
	t.Parallel()

	r := newRegistry(t, nil, time.Minute)
	ctx := context.Background()

	if _, err := r.Register(ctx, rec("run-1", runtime.DisconnectCancel)); err != nil {
		t.Fatal(err)
	}
	if r.LocalCount() != 1 {
		t.Fatalf("LocalCount() = %d, want 1", r.LocalCount())
	}

	if err := r.Complete(ctx, "run-1", runtime.StatusCompleted); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	got, _ := r.Get(ctx, "run-1")
	if got.Status != runtime.StatusCompleted {
		t.Errorf("Status = %q, want completed", got.Status)
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt was not set")
	}
	if r.LocalCount() != 0 {
		t.Errorf("LocalCount() = %d after completion, want 0", r.LocalCount())
	}
}

// 元数据过期不该让一个真的跑完了的回合报错。
func TestRegistry_CompleteToleratesExpiredRecord(t *testing.T) {
	t.Parallel()

	r := newRegistry(t, nil, 20*time.Millisecond)
	ctx := context.Background()

	if _, err := r.Register(ctx, rec("run-1", runtime.DisconnectCancel)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)

	if err := r.Complete(ctx, "run-1", runtime.StatusCompleted); err != nil {
		t.Fatalf("Complete() on an expired record error = %v, want nil", err)
	}
}

// TTL 过期后取消命令找不到目标，一个跑了两小时的任务就再也停不下来。
func TestRegistry_TouchRefreshesTTL(t *testing.T) {
	t.Parallel()

	r := newRegistry(t, nil, 80*time.Millisecond)
	ctx := context.Background()

	if _, err := r.Register(ctx, rec("run-1", runtime.DisconnectContinue)); err != nil {
		t.Fatal(err)
	}
	before, err := r.Get(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}

	// 在过期前续期两次，总时长超过单个 TTL
	for range 2 {
		time.Sleep(50 * time.Millisecond)
		if err := r.Touch(ctx, "run-1"); err != nil {
			t.Fatalf("Touch() error = %v", err)
		}
	}

	if _, err := r.Get(ctx, "run-1"); err != nil {
		t.Fatalf("Get() after refreshes error = %v; the run outlived its TTL without renewal", err)
	}
	after, err := r.Get(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if !after.HeartbeatAt.After(before.HeartbeatAt) {
		t.Fatalf("heartbeat did not advance: before=%s after=%s", before.HeartbeatAt, after.HeartbeatAt)
	}
}

func TestRegistry_RecordExpiresWithoutTouch(t *testing.T) {
	t.Parallel()

	r := newRegistry(t, nil, 20*time.Millisecond)
	ctx := context.Background()

	if _, err := r.Register(ctx, rec("run-1", runtime.DisconnectContinue)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)

	if _, err := r.Get(ctx, "run-1"); !errors.Is(err, runtime.ErrRunNotFound) {
		t.Fatalf("Get() error = %v, want the record to have expired", err)
	}
}

func TestRegistry_KeepAliveRenewsUntilContextEnds(t *testing.T) {
	t.Parallel()

	r := newRegistry(t, nil, 100*time.Millisecond)
	ctx := context.Background()

	if _, err := r.Register(ctx, rec("run-1", runtime.DisconnectContinue)); err != nil {
		t.Fatal(err)
	}

	keepCtx, stop := context.WithCancel(ctx)
	go r.KeepAlive(keepCtx, "run-1", 30*time.Millisecond)

	time.Sleep(250 * time.Millisecond)
	if _, err := r.Get(ctx, "run-1"); err != nil {
		t.Fatalf("Get() while KeepAlive was running error = %v", err)
	}

	stop()
	time.Sleep(200 * time.Millisecond)
	if _, err := r.Get(ctx, "run-1"); !errors.Is(err, runtime.ErrRunNotFound) {
		t.Fatalf("Get() error = %v; the record should expire once KeepAlive stops", err)
	}
}

// --- 断连策略 ---

func TestClientDisconnected_CancelPolicyCancelsWhenLastSubscriberLeaves(t *testing.T) {
	t.Parallel()

	r := newRegistry(t, nil, time.Minute)
	ctx := context.Background()

	runCtx, err := r.Register(ctx, rec("run-1", runtime.DisconnectCancel))
	if err != nil {
		t.Fatal(err)
	}

	if err := r.ClientDisconnected(ctx, "run-1", 0); err != nil {
		t.Fatalf("ClientDisconnected() error = %v", err)
	}

	select {
	case <-runCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("cancel policy did not stop the run when the last subscriber left")
	}
}

// 多端同时观察时，关掉一个窗口不该杀掉别人正在看的 run。
func TestClientDisconnected_KeepsRunningWhileOtherSubscribersRemain(t *testing.T) {
	t.Parallel()

	r := newRegistry(t, nil, time.Minute)
	ctx := context.Background()

	runCtx, err := r.Register(ctx, rec("run-1", runtime.DisconnectCancel))
	if err != nil {
		t.Fatal(err)
	}

	if err := r.ClientDisconnected(ctx, "run-1", 1); err != nil {
		t.Fatal(err)
	}

	select {
	case <-runCtx.Done():
		t.Fatal("the run was cancelled while another subscriber was still watching")
	case <-time.After(100 * time.Millisecond):
	}
}

// 长任务：连接可断、任务不断。
func TestClientDisconnected_ContinuePolicyKeepsRunning(t *testing.T) {
	t.Parallel()

	r := newRegistry(t, nil, time.Minute)
	ctx := context.Background()

	runCtx, err := r.Register(ctx, rec("run-1", runtime.DisconnectContinue))
	if err != nil {
		t.Fatal(err)
	}

	if err := r.ClientDisconnected(ctx, "run-1", 0); err != nil {
		t.Fatal(err)
	}

	select {
	case <-runCtx.Done():
		t.Fatal("on_disconnect=continue must survive the client going away")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestClientDisconnected_UnknownRunIsNoop(t *testing.T) {
	t.Parallel()

	r := newRegistry(t, nil, time.Minute)
	if err := r.ClientDisconnected(context.Background(), "nope", 0); err != nil {
		t.Fatalf("ClientDisconnected() on an unknown run error = %v, want nil", err)
	}
}

func TestRunStatus_Terminal(t *testing.T) {
	t.Parallel()

	terminal := []runtime.RunStatus{runtime.StatusCompleted, runtime.StatusCancelled, runtime.StatusFailed}
	for _, s := range terminal {
		if !s.Terminal() {
			t.Errorf("%q.Terminal() = false", s)
		}
	}
	if runtime.StatusRunning.Terminal() {
		t.Error("running must not be terminal")
	}
}

func TestRegistry_ConcurrentRegisterAndCancel(t *testing.T) {
	t.Parallel()

	r := newRegistry(t, nil, time.Minute)
	ctx := context.Background()

	done := make(chan struct{})
	for i := range 20 {
		go func() {
			defer func() { done <- struct{}{} }()

			id := "run-" + string(rune('a'+i))
			if _, err := r.Register(ctx, rec(id, runtime.DisconnectCancel)); err != nil {
				t.Errorf("Register() error = %v", err)
				return
			}
			if err := r.Cancel(ctx, id); err != nil {
				t.Errorf("Cancel() error = %v", err)
			}
		}()
	}
	for range 20 {
		<-done
	}
}
