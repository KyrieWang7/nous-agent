package loop_test

import (
	"errors"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
)

func TestLimits_MaxIterations(t *testing.T) {
	t.Parallel()

	tr := loop.Limits{MaxIterations: 3}.NewTracker()

	for i := range 3 {
		if err := tr.Check(i); err != nil {
			t.Fatalf("Check(%d) error = %v, want nil", i, err)
		}
	}
	if err := tr.Check(3); !errors.Is(err, loop.ErrMaxIterations) {
		t.Fatalf("Check(3) error = %v, want ErrMaxIterations", err)
	}
}

func TestLimits_ZeroMaxIterationsIsUnbounded(t *testing.T) {
	t.Parallel()

	tr := loop.Limits{}.NewTracker()

	if err := tr.Check(10_000); err != nil {
		t.Fatalf("Check() error = %v; MaxIterations 0 means unbounded", err)
	}
}

func TestLimits_Deadline(t *testing.T) {
	t.Parallel()

	tr := loop.Limits{Deadline: 10 * time.Millisecond}.NewTracker()

	if err := tr.Check(0); err != nil {
		t.Fatalf("Check() error = %v, want nil before the deadline", err)
	}

	time.Sleep(30 * time.Millisecond)

	if err := tr.Check(1); !errors.Is(err, loop.ErrDeadline) {
		t.Fatalf("Check() error = %v, want ErrDeadline", err)
	}
}

// 墙钟上限即便在 MaxIterations 为 0（不限轮次）时也必须生效 ——
// 否则一个卡在长工具上的 run 可以无限期烧下去。
func TestLimits_DeadlineAppliesWithUnboundedIterations(t *testing.T) {
	t.Parallel()

	tr := loop.Limits{MaxIterations: 0, Deadline: 5 * time.Millisecond}.NewTracker()
	time.Sleep(20 * time.Millisecond)

	if err := tr.Check(1); !errors.Is(err, loop.ErrDeadline) {
		t.Fatalf("Check() error = %v, want ErrDeadline", err)
	}
}

func TestLimits_CostCeiling(t *testing.T) {
	t.Parallel()

	tr := loop.Limits{MaxCostMicros: 1_000}.NewTracker()

	tr.ObserveCost(600)
	if err := tr.Check(1); err != nil {
		t.Fatalf("Check() error = %v, want nil below the ceiling", err)
	}

	tr.ObserveCost(600) // 累计 1200 > 1000
	if err := tr.Check(2); !errors.Is(err, loop.ErrBudgetExhausted) {
		t.Fatalf("Check() error = %v, want ErrBudgetExhausted", err)
	}
}

func TestLimits_ZeroCostCeilingIsUnbounded(t *testing.T) {
	t.Parallel()

	tr := loop.Limits{}.NewTracker()
	tr.ObserveCost(1 << 40)

	if err := tr.Check(1); err != nil {
		t.Fatalf("Check() error = %v; MaxCostMicros 0 means unbounded", err)
	}
}

func TestLimits_CostAccumulates(t *testing.T) {
	t.Parallel()

	tr := loop.Limits{}.NewTracker()
	tr.ObserveCost(10)
	tr.ObserveCost(32)

	if got := tr.CostMicros(); got != 42 {
		t.Fatalf("CostMicros() = %d, want 42", got)
	}
}

// 三种上限同时触发时，先报轮次 —— 它是最常见也最好懂的那个。
func TestLimits_IterationTakesPrecedence(t *testing.T) {
	t.Parallel()

	tr := loop.Limits{MaxIterations: 1, Deadline: time.Nanosecond, MaxCostMicros: 1}.NewTracker()
	tr.ObserveCost(999)
	time.Sleep(time.Millisecond)

	if err := tr.Check(1); !errors.Is(err, loop.ErrMaxIterations) {
		t.Fatalf("Check() error = %v, want ErrMaxIterations to take precedence", err)
	}
}

func TestLimits_ConcurrentObserveCost(t *testing.T) {
	t.Parallel()

	tr := loop.Limits{}.NewTracker()

	done := make(chan struct{})
	for range 50 {
		go func() {
			defer func() { done <- struct{}{} }()
			tr.ObserveCost(2)
			_ = tr.CostMicros()
		}()
	}
	for range 50 {
		<-done
	}

	if got := tr.CostMicros(); got != 100 {
		t.Fatalf("CostMicros() = %d, want 100", got)
	}
}
