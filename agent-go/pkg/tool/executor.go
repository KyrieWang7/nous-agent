package tool

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
)

// Outcome keeps model order even when tool bodies complete out of order.
type Outcome struct {
	Call    Call
	Result  *Result
	ExecErr error
}

type ExecutorOptions struct {
	// Concurrency bounds dispatched bodies; <=0 allows the entire parallel run.
	Concurrency int
}

type Executor struct {
	registry *Registry
	opts     ExecutorOptions
	endTurn  atomic.Bool
}

func NewExecutor(r *Registry, opts ExecutorOptions) *Executor {
	return &Executor{registry: r, opts: opts}
}
func (e *Executor) EndTurnRequested() bool { return e.endTurn.Load() }

type invocation struct {
	call       Call
	ctx        context.Context
	tx         Transaction
	result     *Result
	execErr    error
	prepareErr error
	deny       string
	stop       bool
	ready      bool
}
type bodyResult struct {
	index  int
	result *Result
	err    error
}

// Run follows DSH's rolling pool: prepare in model order, overlap bodies,
// finalize contiguous ready results in model order, and reclassify unstarted
// calls against the live registry. Exclusive calls are full settlement barriers.
// Cancellation/failure stops replenishment and drains every dispatched body.
// BatchResult owns the stop decision for this batch. Shared executors must use
// it instead of reading the legacy EndTurnRequested latch from another run.
type BatchResult struct {
	Outcomes []Outcome
	EndTurn  bool
}

func (e *Executor) Run(ctx context.Context, calls []Call, ic Interceptor) ([]Outcome, error) {
	e.endTurn.Store(false)
	result, err := e.RunBatch(ctx, calls, ic)
	e.endTurn.Store(result.EndTurn)
	return result.Outcomes, err
}
func (e *Executor) RunBatch(ctx context.Context, calls []Call, ic Interceptor) (BatchResult, error) {
	var endTurn atomic.Bool
	outcomes, err := e.run(ctx, calls, ic, &endTurn)
	return BatchResult{Outcomes: outcomes, EndTurn: endTurn.Load()}, err
}
func (e *Executor) run(ctx context.Context, calls []Call, ic Interceptor, endTurn *atomic.Bool) ([]Outcome, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	group, cancel := context.WithCancel(ctx)
	defer cancel()
	limit := e.opts.Concurrency
	if limit <= 0 {
		limit = len(calls)
	}
	slots := make([]*invocation, len(calls))
	outcomes := make([]Outcome, len(calls))
	done := make(chan bodyResult, len(calls))
	next, committed, active, bound := 0, 0, 0, 0
	exclusive := false
	stopped := false
	var firstErr error
	fail := func(err error) {
		if err != nil {
			canceled := errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
			// Cancellation we induced to drain peers must not reclassify a policy or
			// persistence failure (or an EndTurn decision) as external cancellation.
			if !canceled || ctx.Err() != nil || (firstErr == nil && !endTurn.Load()) {
				if firstErr == nil {
					firstErr = err
				} else if !errors.Is(firstErr, err) {
					firstErr = errors.Join(firstErr, err)
				}
			}
		}
		stopped = true
		cancel()
	}
	commitReady := func() {
		for committed < next && slots[committed].ready {
			slot := slots[committed]
			outcome, err := e.complete(group, slot, ic)
			if err != nil {
				fail(err)
			} else {
				outcomes[committed] = outcome
				bound = committed + 1
			}
			committed++
		}
	}
	for next < len(calls) || active > 0 {
		if err := ctx.Err(); err != nil {
			fail(err)
		}
		commitReady()
		// Only actually dispatched bodies occupy the pool. A fast later call frees
		// capacity even when an earlier result is still waiting to be committed.
		for !stopped && next < len(calls) && active < limit && !exclusive {
			parallel := concurrentSafe(e.registry, calls[next].Name)
			if !parallel && (active > 0 || committed < next) {
				break
			}
			slot := e.prepare(group, ctx, calls[next], ic)
			slots[next] = slot
			index := next
			next++
			if slot.prepareErr != nil {
				slot.ready = true
				fail(slot.prepareErr)
			}
			if slot.stop {
				endTurn.Store(true)
				stopped = true
				cancel()
			}
			if slot.deny != "" {
				slot.ready = true
			}
			if !slot.ready {
				// Recheck cancellation after an awaiting policy stage. Never dispatch a
				// body accepted after its owning execution has already been canceled.
				if err := group.Err(); err != nil {
					slot.prepareErr = err
					slot.ready = true
					fail(err)
				} else {
					active++
					exclusive = !parallel
					go func(index int, slot *invocation) {
						res, err := e.callHandler(slot.ctx, slot.call)
						done <- bodyResult{index, res, err}
					}(index, slot)
				}
			}
			commitReady()
		}
		if active == 0 {
			commitReady()
			if stopped {
				break
			}
			continue
		}
		// Even after cancellation we receive every started body before returning.
		completed := <-done
		active--
		if exclusive {
			exclusive = false
		}
		slot := slots[completed.index]
		slot.result, slot.execErr, slot.ready = completed.result, completed.err, true
		if errors.Is(completed.err, context.Canceled) || errors.Is(completed.err, context.DeadlineExceeded) {
			if !endTurn.Load() {
				fail(completed.err)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		fail(err)
	}
	commitReady()
	return outcomes[:bound], firstErr
}

// prepare owns policy/transaction admission; it never executes a tool body.
func (e *Executor) prepare(ctx context.Context, auditCtx context.Context, c Call, ic Interceptor) *invocation {
	slot := &invocation{call: c, ctx: ctx}
	if observer := transactionObserverFrom(ctx); observer != nil {
		slot.tx, slot.prepareErr = observer.Start(auditCtx, c)
		if slot.prepareErr != nil {
			return slot
		}
		if slot.tx == nil {
			slot.prepareErr = errors.New("tool: transaction observer returned nil transaction")
			return slot
		}
	}
	if ic == nil {
		return slot
	}
	decision, err := ic.BeforeTool(ctx, c)
	if err != nil {
		slot.prepareErr = err
		return slot
	}
	if decision.EndTurn {
		slot.stop = true
		slot.deny = reasonOr(decision.Reason, "turn ended by interceptor")
		return slot
	}
	if decision.Deny {
		slot.deny = reasonOr(decision.Reason, "tool call denied")
		return slot
	}
	if decision.Args != nil {
		slot.call.Args = decision.Args
	}
	if decision.SandboxMode != "" {
		slot.ctx = sandbox.WithMode(ctx, sandbox.Mode(decision.SandboxMode))
	}
	return slot
}

// complete owns result policy and durable settlement. The scheduler is its sole
// caller, so shared lifecycle state and result observers see deterministic order.
func (e *Executor) complete(ctx context.Context, slot *invocation, ic Interceptor) (Outcome, error) {
	if slot.prepareErr != nil {
		err := slot.prepareErr
		if slot.tx != nil {
			err = errors.Join(err, slot.tx.Cancel(err))
		}
		return Outcome{}, err
	}
	if slot.deny != "" {
		if slot.tx != nil {
			if err := slot.tx.Deny(slot.deny); err != nil {
				return Outcome{}, err
			}
		}
		return Outcome{Call: slot.call, Result: &Result{Content: slot.deny, IsError: true}}, nil
	}
	// Once the pipeline is aborted, late bodies are drained but not admitted.
	// The step owner records conservative recovery for their pending results.
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	res, execErr := slot.result, slot.execErr
	if ic != nil {
		rewritten, err := ic.AfterTool(ctx, slot.call, res, execErr)
		if err != nil {
			// A canceled policy phase did not admit this output. Leave its transaction
			// to the owning step's conservative recovery rather than publish raw data.
			if ctx.Err() != nil {
				return Outcome{}, err
			}
			if slot.tx != nil {
				err = errors.Join(err, slot.tx.Finish(res, err))
			}
			return Outcome{}, err
		}
		if rewritten != nil {
			res = rewritten
		}
	}
	if res == nil {
		res = errorResult(execErr)
	}
	if errors.Is(execErr, context.Canceled) || errors.Is(execErr, context.DeadlineExceeded) {
		if slot.tx != nil {
			execErr = errors.Join(execErr, slot.tx.Cancel(execErr))
		}
		return Outcome{}, execErr
	}
	if slot.tx != nil {
		if err := slot.tx.Finish(res, execErr); err != nil {
			return Outcome{}, err
		}
	}
	return Outcome{Call: slot.call, Result: res, ExecErr: execErr}, nil
}

func (e *Executor) callHandler(ctx context.Context, c Call) (res *Result, err error) {
	d, err := e.registry.Get(c.Name)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cause := recover(); cause != nil {
			res = nil
			err = fmt.Errorf("tool %q panicked: %v", c.Name, cause)
		}
	}()
	return d.Handler(ctx, c)
}
func errorResult(err error) *Result {
	msg := "tool returned no result"
	if err != nil {
		msg = err.Error()
	}
	return &Result{Content: msg, IsError: true}
}
func reasonOr(reason, fallback string) string {
	if reason != "" {
		return reason
	}
	return fallback
}
