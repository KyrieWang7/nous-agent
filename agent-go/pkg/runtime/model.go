package runtime

import (
	"context"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

// InstrumentModel attributes auxiliary model calls to a run Journal. It is
// used for compaction, title, memory, and guardrail calls; lead-loop accounting
// remains in TokenUsage middleware.
func InstrumentModel(inner model.Model, bucket Bucket, source string) model.Model {
	if inner == nil {
		return nil
	}
	return &accountingModel{inner: inner, bucket: bucket, source: source}
}

type accountingModel struct {
	inner  model.Model
	bucket Bucket
	source string
}

func (m *accountingModel) Info() model.Info { return m.inner.Info() }

func (m *accountingModel) Complete(ctx context.Context, req model.Request) (*model.Response, error) {
	resp, err := m.inner.Complete(ctx, req)
	if err == nil {
		m.observe(ctx, resp)
	}
	return resp, err
}

func (m *accountingModel) Stream(ctx context.Context, req model.Request) (model.StreamReader, error) {
	reader, err := m.inner.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	return &accountingReader{StreamReader: reader, ctx: ctx, owner: m}, nil
}

func (m *accountingModel) observe(ctx context.Context, resp *model.Response) {
	if resp == nil {
		return
	}
	run, ok := RunContextFrom(ctx)
	if !ok || run.Journal == nil {
		return
	}
	run.Journal.Observe(Entry{Bucket: m.bucket, Source: m.source, CallID: resp.CallID, ModelName: resp.ModelName, Usage: resp.Usage})
}

type accountingReader struct {
	model.StreamReader
	ctx   context.Context
	owner *accountingModel
}

func (r *accountingReader) Result() (*model.Response, error) {
	resp, err := r.StreamReader.Result()
	if err == nil {
		r.owner.observe(r.ctx, resp)
	}
	return resp, err
}
