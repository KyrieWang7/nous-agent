package replay

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

type BudgetProjector struct {
	mu       sync.Mutex
	snapshot runtime.BudgetSnapshot
	lastSeq  int64
}

func NewBudgetProjector() *BudgetProjector { return &BudgetProjector{} }

func (p *BudgetProjector) Apply(event runtime.Event) error {
	if p == nil {
		return errors.New("replay: budget projector is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if event.Seq > 0 && event.Seq <= p.lastSeq {
		return nil
	}
	if event.Type == runtime.EventBudgetChanged {
		var snapshot runtime.BudgetSnapshot
		if err := json.Unmarshal(event.Data, &snapshot); err != nil {
			return fmt.Errorf("replay: decoding budget event seq %d: %w", event.Seq, err)
		}
		if snapshot.Version >= p.snapshot.Version {
			p.snapshot = snapshot
		}
	}
	if event.Seq > p.lastSeq {
		p.lastSeq = event.Seq
	}
	return nil
}

func (p *BudgetProjector) Snapshot() runtime.BudgetSnapshot {
	if p == nil {
		return runtime.BudgetSnapshot{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshot
}
