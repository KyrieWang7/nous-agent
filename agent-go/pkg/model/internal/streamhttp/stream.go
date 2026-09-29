// Package streamhttp separates stream inactivity from ordinary request deadlines.
package streamhttp

import (
	"context"
	"errors"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"io"
	"net/http"
	"sync"
	"time"
)

var ErrIdleTimeout = errors.New("model stream idle timeout")

type body struct {
	io.ReadCloser
	ctx    context.Context
	cancel context.CancelCauseFunc
	mu     sync.Mutex
	timer  *time.Timer
	idle   time.Duration
	last   time.Time
	closed bool
}

func (b *body) expire() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	remaining := b.idle - time.Since(b.last)
	if remaining > 0 {
		b.timer.Reset(remaining)
		return
	}
	b.closed = true
	b.cancel(errors.Join(model.ErrProviderUnavailable, ErrIdleTimeout))
}
func (b *body) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	if n > 0 && !b.closed {
		b.last = time.Now()
	}
	b.mu.Unlock()
	if err != nil && b.ctx.Err() != nil {
		err = context.Cause(b.ctx)
	}
	return n, err
}
func (b *body) Close() error {
	b.mu.Lock()
	b.closed = true
	b.timer.Stop()
	b.mu.Unlock()
	b.cancel(context.Canceled)
	return b.ReadCloser.Close()
}

// Do starts the idle clock before headers arrive and resets activity on body
// reads (including SSE keepalives). The caller's deadline always takes priority.
// The returned body must be closed by the stream owner.
func Do(client *http.Client, req *http.Request, idle time.Duration) (*http.Response, error) {
	if idle <= 0 {
		idle = 120 * time.Second
	}
	ctx, cancel := context.WithCancelCause(req.Context())
	b := &body{ctx: ctx, cancel: cancel, idle: idle, last: time.Now()}
	b.mu.Lock()
	b.timer = time.AfterFunc(idle, b.expire)
	b.mu.Unlock()
	cloned := *client
	cloned.Timeout = 0
	resp, err := cloned.Do(req.Clone(ctx))
	if err != nil {
		b.mu.Lock()
		b.closed = true
		b.timer.Stop()
		b.mu.Unlock()
		cause := context.Cause(ctx)
		cancel(context.Canceled)
		if cause != nil {
			return nil, cause
		}
		return nil, err
	}
	b.ReadCloser = resp.Body
	resp.Body = b
	return resp, nil
}
