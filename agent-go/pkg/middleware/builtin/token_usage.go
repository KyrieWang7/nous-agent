package builtin

import (
	"context"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

const NameTokenUsage = "tokenUsage"

type TokenUsage struct{}

func NewTokenUsage() *TokenUsage           { return &TokenUsage{} }
func (TokenUsage) Name() string            { return NameTokenUsage }
func (TokenUsage) Grade() middleware.Grade { return middleware.GradeListener }
func (TokenUsage) AfterModel(ctx context.Context, st *middleware.State) error {
	if st.ModelOutput == nil {
		return nil
	}
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.Journal == nil {
		return nil
	}
	run.Journal.Observe(runtime.Entry{
		Bucket:    runtime.BucketLead,
		Source:    "lead",
		CallID:    st.ModelOutput.CallID,
		ModelName: st.ModelOutput.ModelName,
		Usage:     st.ModelOutput.Usage,
	})
	return nil
}
