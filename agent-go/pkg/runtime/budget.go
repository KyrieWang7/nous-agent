package runtime

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// BudgetAmount is the accounting unit shared by model and child-agent
// capability calls. A zero dimension means that dimension is not charged.
type BudgetAmount struct {
	Tokens     int64 `json:"tokens"`
	CostMicros int64 `json:"cost_micros"`
	ToolCalls  int64 `json:"tool_calls"`
	Subagents  int64 `json:"subagents"`
}

func (a BudgetAmount) Add(b BudgetAmount) BudgetAmount {
	return BudgetAmount{Tokens: a.Tokens + b.Tokens, CostMicros: a.CostMicros + b.CostMicros, ToolCalls: a.ToolCalls + b.ToolCalls, Subagents: a.Subagents + b.Subagents}
}

var ErrBudgetExceeded = errors.New("runtime: budget exceeded")

type budgetBook struct{ mu sync.Mutex }

type BudgetLedger struct {
	book     *budgetBook
	parent   *BudgetLedger
	max      BudgetAmount
	used     BudgetAmount
	resv     BudgetAmount
	version  uint64
	updated  time.Time
	observer func(BudgetSnapshot) error
}

type BudgetSnapshot struct {
	RunID     string       `json:"run_id,omitempty"`
	ThreadID  string       `json:"thread_id,omitempty"`
	Max       BudgetAmount `json:"max"`
	Used      BudgetAmount `json:"used"`
	Version   uint64       `json:"version"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// NewBudgetLedger creates a root ledger. Limits <= 0 are unlimited.
func NewBudgetLedger(max BudgetAmount) *BudgetLedger {
	return &BudgetLedger{book: &budgetBook{}, max: normalizeBudget(max), updated: time.Now().UTC()}
}

func RestoreBudgetLedger(snapshot BudgetSnapshot) (*BudgetLedger, error) {
	max := normalizeBudget(snapshot.Max)
	used := normalizeBudget(snapshot.Used)
	if exceeds(max.Tokens, used.Tokens) || exceeds(max.CostMicros, used.CostMicros) || exceeds(max.ToolCalls, used.ToolCalls) || exceeds(max.Subagents, used.Subagents) {
		return nil, errors.New("runtime: restored budget usage exceeds its limit")
	}
	updated := snapshot.UpdatedAt
	if updated.IsZero() {
		updated = time.Now().UTC()
	}
	return &BudgetLedger{book: &budgetBook{}, max: max, used: used, version: snapshot.Version, updated: updated}, nil
}

// Child creates a ledger whose reservations count against its own limit and
// every ancestor. This is the parent/child budget boundary for subagents.
func (l *BudgetLedger) Child(max BudgetAmount) (*BudgetLedger, error) {
	if l == nil {
		return nil, errors.New("runtime: nil parent budget ledger")
	}
	return &BudgetLedger{book: l.book, parent: l, max: normalizeBudget(max), updated: time.Now().UTC()}, nil
}

func (l *BudgetLedger) SetObserver(observer func(BudgetSnapshot) error) {
	if l == nil || l.book == nil {
		return
	}
	l.book.mu.Lock()
	l.observer = observer
	l.book.mu.Unlock()
}

func (l *BudgetLedger) Snapshot() BudgetSnapshot {
	if l == nil || l.book == nil {
		return BudgetSnapshot{}
	}
	l.book.mu.Lock()
	defer l.book.mu.Unlock()
	return l.snapshotLocked()
}

func (l *BudgetLedger) snapshotLocked() BudgetSnapshot {
	return BudgetSnapshot{Max: l.max, Used: l.used, Version: l.version, UpdatedAt: l.updated}
}

func (l *BudgetLedger) Used() BudgetAmount {
	if l == nil || l.book == nil {
		return BudgetAmount{}
	}
	l.book.mu.Lock()
	defer l.book.mu.Unlock()
	return l.used
}

func (l *BudgetLedger) Remaining() BudgetAmount {
	if l == nil || l.book == nil {
		return BudgetAmount{}
	}
	l.book.mu.Lock()
	defer l.book.mu.Unlock()
	return BudgetAmount{
		Tokens:     remaining(l.max.Tokens, l.used.Tokens+l.resv.Tokens),
		CostMicros: remaining(l.max.CostMicros, l.used.CostMicros+l.resv.CostMicros),
		ToolCalls:  remaining(l.max.ToolCalls, l.used.ToolCalls+l.resv.ToolCalls),
		Subagents:  remaining(l.max.Subagents, l.used.Subagents+l.resv.Subagents),
	}
}

// Reserve atomically reserves an estimate along the whole ancestor path.
// Reservations must be committed or released; each operation is idempotent.
func (l *BudgetLedger) Reserve(amount BudgetAmount) (*BudgetReservation, error) {
	if l == nil || l.book == nil {
		return nil, errors.New("runtime: nil budget ledger")
	}
	amount = normalizeBudget(amount)
	l.book.mu.Lock()
	defer l.book.mu.Unlock()
	path := ledgerPath(l)
	for _, node := range path {
		if exceeds(node.max.Tokens, node.used.Tokens+node.resv.Tokens+amount.Tokens) || exceeds(node.max.CostMicros, node.used.CostMicros+node.resv.CostMicros+amount.CostMicros) || exceeds(node.max.ToolCalls, node.used.ToolCalls+node.resv.ToolCalls+amount.ToolCalls) || exceeds(node.max.Subagents, node.used.Subagents+node.resv.Subagents+amount.Subagents) {
			return nil, fmt.Errorf("%w: requested tokens=%d cost_micros=%d", ErrBudgetExceeded, amount.Tokens, amount.CostMicros)
		}
	}
	for _, node := range path {
		node.resv = node.resv.Add(amount)
	}
	return &BudgetReservation{ledger: l, amount: amount}, nil
}

// Charge is a convenience for completed usage when no pre-reservation exists.
func (l *BudgetLedger) Charge(amount BudgetAmount) error {
	r, err := l.Reserve(amount)
	if err != nil {
		return err
	}
	return r.Commit()
}

type BudgetReservation struct {
	mu       sync.Mutex
	ledger   *BudgetLedger
	amount   BudgetAmount
	finished bool
}

func (r *BudgetReservation) Commit() error  { return r.finish(true) }
func (r *BudgetReservation) Release() error { return r.finish(false) }

func (r *BudgetReservation) finish(commit bool) error {
	if r == nil || r.ledger == nil {
		return errors.New("runtime: nil budget reservation")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return nil
	}
	r.ledger.book.mu.Lock()
	path := ledgerPath(r.ledger)
	if commit {
		now := time.Now().UTC()
		snapshots := make([]BudgetSnapshot, len(path))
		for i, node := range path {
			snapshots[i] = BudgetSnapshot{
				Max:       node.max,
				Used:      node.used.Add(r.amount),
				Version:   node.version + 1,
				UpdatedAt: now,
			}
		}
		// A checkpoint is a canonical fact. Persist every observed snapshot
		// before making the corresponding in-memory charge visible. Observers
		// must not call back into this ledger because the shared book is locked
		// to keep parent and child commits atomic.
		for i, node := range path {
			if node.observer == nil {
				continue
			}
			if err := node.observer(snapshots[i]); err != nil {
				for _, reserved := range path {
					reserved.resv = subtractBudget(reserved.resv, r.amount)
				}
				r.finished = true
				r.ledger.book.mu.Unlock()
				return fmt.Errorf("runtime: persisting budget checkpoint: %w", err)
			}
		}
		for i, node := range path {
			node.used = snapshots[i].Used
			node.version = snapshots[i].Version
			node.updated = snapshots[i].UpdatedAt
		}
	}
	for _, node := range path {
		node.resv = subtractBudget(node.resv, r.amount)
	}
	r.finished = true
	r.ledger.book.mu.Unlock()
	return nil
}

func subtractBudget(a, b BudgetAmount) BudgetAmount {
	return BudgetAmount{
		Tokens:     a.Tokens - b.Tokens,
		CostMicros: a.CostMicros - b.CostMicros,
		ToolCalls:  a.ToolCalls - b.ToolCalls,
		Subagents:  a.Subagents - b.Subagents,
	}
}

func ledgerPath(l *BudgetLedger) []*BudgetLedger {
	var reversed []*BudgetLedger
	for current := l; current != nil; current = current.parent {
		reversed = append(reversed, current)
	}
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed
}

func normalizeBudget(a BudgetAmount) BudgetAmount {
	if a.Tokens < 0 {
		a.Tokens = 0
	}
	if a.CostMicros < 0 {
		a.CostMicros = 0
	}
	if a.ToolCalls < 0 {
		a.ToolCalls = 0
	}
	if a.Subagents < 0 {
		a.Subagents = 0
	}
	return a
}

func exceeds(max, value int64) bool { return max > 0 && value > max }

func remaining(max, value int64) int64 {
	if max <= 0 {
		return 0
	}
	if value >= max {
		return 0
	}
	return max - value
}
