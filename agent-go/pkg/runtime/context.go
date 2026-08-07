package runtime

import "context"

type RunContext struct {
	RunID        string
	ThreadID     string
	AllowedTools []string
	Journal      *Journal
	Bus          Bus
	// Publish is the authoritative event path for a run. HTTP adapters use it
	// to fan out to the live bus and durable replay store with one sequence.
	Publish func(context.Context, Event) int64
}
type runContextKey struct{}

func WithRunContext(ctx context.Context, r RunContext) context.Context {
	return context.WithValue(ctx, runContextKey{}, r)
}
func RunContextFrom(ctx context.Context) (RunContext, bool) {
	r, ok := ctx.Value(runContextKey{}).(RunContext)
	return r, ok
}
