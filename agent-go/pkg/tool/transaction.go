package tool

import "context"

// Transaction is the lifecycle handle for one tool invocation. The executor
// owns when these methods are called; observers own persistence/audit policy.
type Transaction interface {
	Finish(*Result, error) error
	Deny(string) error
	Cancel(error) error
}

// TransactionObserver creates a transaction before a handler is invoked. It
// is deliberately defined in the tool package so custom executors can support
// the same lifecycle without importing the runtime implementation.
type TransactionObserver interface {
	Start(context.Context, Call) (Transaction, error)
}

type transactionObserverKey struct{}

// WithTransactionObserver attaches the transaction observer selected by the
// caller. The Kernel installs one for every tool execution batch.
func WithTransactionObserver(ctx context.Context, observer TransactionObserver) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, transactionObserverKey{}, observer)
}

func transactionObserverFrom(ctx context.Context) TransactionObserver {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(transactionObserverKey{}).(TransactionObserver)
	return observer
}
