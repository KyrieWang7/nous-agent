package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
)

// PreparedCall freezes an adapter request before the loop admits user input.
// Preparing may validate/serialize input, but must not start model generation.
// Each retry/fallback prepares a new call before its own input-commit barrier.
type PreparedCall interface {
	Info() Info
	Complete(context.Context) (*Response, error)
	Stream(context.Context) (StreamReader, error)
}

type CallPreparer interface {
	PrepareCall(context.Context, Request) (PreparedCall, error)
}

// BoundCall is the adapter implementation for an already serialized request.
type BoundCall struct {
	ModelInfo    Info
	CompleteFunc func(context.Context) (*Response, error)
	StreamFunc   func(context.Context) (StreamReader, error)
}

func (c BoundCall) Info() Info                                       { return c.ModelInfo }
func (c BoundCall) Complete(ctx context.Context) (*Response, error)  { return c.CompleteFunc(ctx) }
func (c BoundCall) Stream(ctx context.Context) (StreamReader, error) { return c.StreamFunc(ctx) }

func PrepareCall(ctx context.Context, m Model, req Request) (PreparedCall, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Freeze JSON input for both native adapters and custom providers. In
	// particular a file-reference recovery must reuse the admitted request.
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var frozen Request
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&frozen); err != nil {
		return nil, err
	}
	if p, ok := m.(CallPreparer); ok {
		call, err := p.PrepareCall(ctx, frozen)
		if err != nil {
			return nil, err
		}
		if call == nil {
			return nil, errors.New("model: preparer returned no call")
		}
		return call, nil
	}
	return BoundCall{ModelInfo: RequestInfo(m, frozen),
		CompleteFunc: func(ctx context.Context) (*Response, error) { return m.Complete(ctx, frozen) },
		StreamFunc:   func(ctx context.Context) (StreamReader, error) { return m.Stream(ctx, frozen) },
	}, nil
}
