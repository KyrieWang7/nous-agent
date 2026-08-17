package loop

import (
	"context"
	"errors"
	"sync"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// runtimeToolTransactionObserver bridges the kernel's explicit tool step to
// the runtime transaction ledger. The tool package only sees its neutral hook
// interface, so custom executors remain free of runtime implementation types.
type runtimeToolTransactionObserver struct {
	run runtime.RunContext
}

func newRuntimeToolTransactionObserver(run runtime.RunContext) tool.TransactionObserver {
	return &runtimeToolTransactionObserver{run: run}
}

func (o *runtimeToolTransactionObserver) Start(ctx context.Context, call tool.Call) (tool.Transaction, error) {
	callID := call.ID
	if callID == "" {
		return nil, errors.New("loop: tool transaction requires a normalized call id")
	}
	txID := o.run.RunID + ":tool:" + callID
	tx, err := runtime.NewToolTransaction(txID, o.run.RunID, runtime.ToolInvocation{ID: callID, Name: call.Name, Args: call.Args})
	if err != nil {
		return nil, err
	}
	if err := tx.Begin(); err != nil {
		return nil, err
	}
	h := &runtimeToolTransaction{observer: o, tx: tx, call: call, txID: txID, ctx: ctx}
	if err := o.publish(ctx, runtime.EventToolStart, runtime.ToolStart{ToolCallID: callID, Name: call.Name, Args: call.Args}, txID+":start"); err != nil {
		return nil, err
	}
	return h, nil
}

type runtimeToolTransaction struct {
	mu       sync.Mutex
	observer *runtimeToolTransactionObserver
	tx       *runtime.ToolTransaction
	call     tool.Call
	txID     string
	ctx      context.Context
	terminal bool
}

func (t *runtimeToolTransaction) Finish(ctxResult *tool.Result, execErr error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.terminal {
		return nil
	}
	var txErr error
	if execErr != nil {
		txErr = t.tx.Fail(execErr, ctxResult)
	} else {
		txErr = t.tx.Complete(ctxResult)
	}
	if txErr != nil {
		return txErr
	}
	t.terminal = true
	return t.publishResult(ctxResult, execErr)
}

func (t *runtimeToolTransaction) Deny(reason string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.terminal {
		return nil
	}
	if err := t.tx.Deny(reason); err != nil {
		return err
	}
	t.terminal = true
	return t.publishResult(&tool.Result{Content: reason, IsError: true}, nil)
}

func (t *runtimeToolTransaction) Cancel(err error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.terminal {
		return nil
	}
	if txErr := t.tx.Cancel(errorText(err)); txErr != nil {
		return txErr
	}
	t.terminal = true
	return t.publishResult(&tool.Result{Content: errorText(err), IsError: true}, err)
}

func (t *runtimeToolTransaction) publishResult(result *tool.Result, execErr error) error {
	content := ""
	isError := execErr != nil
	if result != nil {
		content = result.Content
		isError = isError || result.IsError
	}
	if content == "" && execErr != nil {
		content = execErr.Error()
	}
	return t.observer.publish(t.ctx, runtime.EventToolResult, runtime.ToolResult{
		ToolCallID: t.call.ID,
		Name:       t.call.Name,
		Content:    content,
		IsError:    isError,
	}, t.txID+":result")
}

func (o *runtimeToolTransactionObserver) publish(ctx context.Context, typ runtime.EventType, payload any, key string) error {
	e := runtime.MustEvent(o.run.EventStreamRunID(), o.run.ThreadID, typ, payload)
	e.IdempotencyKey = key
	if o.run.Publish != nil {
		_, err := o.run.Publish(ctx, e)
		return err
	}
	if o.run.Bus != nil {
		o.run.Bus.Publish(ctx, e)
	}
	return nil
}

func errorText(err error) string {
	if err == nil {
		return "tool call cancelled"
	}
	return err.Error()
}
