// Package scope owns registrations and background work as one disposable unit.
// It is the Go lifecycle counterpart of dsh-scope, independent of HTTP and plugins.
package scope

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrDisposed = errors.New("scope: already disposed")

type Cleanup func(context.Context) error

// Scope cancels owned work, releases effects in reverse order, and waits for
// quiescence. All racing Dispose calls observe the same completion and error.
type Scope struct {
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	closed  bool
	effects []Cleanup
	workers sync.WaitGroup
	done    chan struct{}
	err     error
}

func New(parent context.Context) *Scope {
	ctx, cancel := context.WithCancel(parent)
	return &Scope{ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

func (s *Scope) Context() context.Context { return s.ctx }

// Own installs a registration and its disposer atomically with respect to
// disposal. acquire must only register the effect; it must not call back into s.
func (s *Scope) Own(acquire func() (Cleanup, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrDisposed
	}
	cleanup, err := acquire()
	if err != nil {
		return err
	}
	if cleanup != nil {
		s.effects = append(s.effects, cleanup)
	}
	return nil
}

func (s *Scope) OnDispose(cleanup Cleanup) error {
	return s.Own(func() (Cleanup, error) { return cleanup, nil })
}

// Child is disposed with its owner, even if setup fails before publication.
func (s *Scope) Child() (*Scope, error) {
	var child *Scope
	err := s.Own(func() (Cleanup, error) {
		child = New(s.ctx)
		return func(context.Context) error { return child.Dispose() }, nil //nolint:contextcheck // child owns its cleanup context
	})
	return child, err
}

// Go starts owned work. Work must return after cancellation or effect cleanup.
// It must not dispose its own scope (which would wait for itself).
func (s *Scope) Go(work func(context.Context)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrDisposed
	}
	s.workers.Add(1)
	go func() { defer s.workers.Done(); work(s.ctx) }()
	return nil
}

func (s *Scope) Dispose() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		<-s.done
		return s.err
	}
	s.closed = true
	effects := s.effects
	s.effects = nil
	s.mu.Unlock()
	s.cancel()
	var errs []error
	for i := len(effects) - 1; i >= 0; i-- {
		if err := runCleanup(effects[i], context.WithoutCancel(s.ctx)); err != nil {
			errs = append(errs, err)
		}
	}
	s.workers.Wait()
	s.err = errors.Join(errs...)
	close(s.done)
	return s.err
}

// One faulty disposer must not strand sibling resources or racing callers.
func runCleanup(cleanup Cleanup, ctx context.Context) (err error) {
	defer func() {
		if cause := recover(); cause != nil {
			err = fmt.Errorf("scope: cleanup panic: %v", cause)
		}
	}()
	return cleanup(ctx)
}
