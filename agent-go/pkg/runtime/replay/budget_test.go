package replay_test

import (
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/replay"
)

func TestBudgetProjectorUsesLatestVersion(t *testing.T) {
	projector := replay.NewBudgetProjector()
	newer := runtime.BudgetSnapshot{RunID: "run-1", Version: 2, Used: runtime.BudgetAmount{Tokens: 7}, UpdatedAt: time.Now().UTC()}
	older := runtime.BudgetSnapshot{RunID: "run-1", Version: 1, Used: runtime.BudgetAmount{Tokens: 3}, UpdatedAt: time.Now().UTC()}
	eventNew := runtime.MustEvent("run-1", "thread-1", runtime.EventBudgetChanged, newer)
	eventNew.Seq = 1
	eventOld := runtime.MustEvent("run-1", "thread-1", runtime.EventBudgetChanged, older)
	eventOld.Seq = 2
	if err := projector.Apply(eventNew); err != nil {
		t.Fatal(err)
	}
	if err := projector.Apply(eventOld); err != nil {
		t.Fatal(err)
	}
	if got := projector.Snapshot(); got.Version != 2 || got.Used.Tokens != 7 {
		t.Fatalf("projected budget = %+v", got)
	}
}
