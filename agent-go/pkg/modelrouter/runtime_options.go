package modelrouter

import (
	"context"
	"fmt"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

const (
	// NameRuntimeOptions is the middleware identity used by declarative chains.
	NameRuntimeOptions = "modelRuntimeOptions"

	ValueModelName       = "model_name"
	ValueThinkingEnabled = "thinking_enabled"
	ValueReasoningEffort = "reasoning_effort"
	ValueSupportsVision  = "supports_vision"
)

// RuntimeOptions applies run-scoped model settings to each model request.
//
// Construct it from the same Router used by the loop so capability decisions
// and sampling cannot resolve different named models. BeforeAgent publishes
// vision support early enough for ViewImage regardless of middleware order;
// BeforeModel applies request fields after the loop has built ModelInput.
type RuntimeOptions struct {
	router *Router
}

// RuntimeOptions returns middleware backed by this router's model catalog.
func (r *Router) RuntimeOptions() *RuntimeOptions {
	return &RuntimeOptions{router: r}
}

func (*RuntimeOptions) Name() string { return NameRuntimeOptions }

func (o *RuntimeOptions) BeforeAgent(_ context.Context, st *middleware.State) error {
	return o.applyPreferred(st)
}

func (o *RuntimeOptions) BeforeModel(_ context.Context, st *middleware.State) error {
	return o.applyPreferred(st)
}

func (o *RuntimeOptions) applyPreferred(st *middleware.State) error {
	if o == nil || o.router == nil {
		return fmt.Errorf("modelrouter: runtime options require a router")
	}
	if st == nil {
		return fmt.Errorf("modelrouter: runtime options require state")
	}
	m, err := o.router.preferredModel(st)
	if err != nil {
		return err
	}
	return applyRuntimeOptions(st, m)
}

func (r *Router) preferredModel(st *middleware.State) (model.Model, error) {
	if name := selectedModelName(st); name != "" {
		return r.namedModel(name)
	}

	for _, tier := range r.chainFor(r.tierFor(st)) {
		if m, ok := r.cfg.Models[tier]; ok {
			return m, nil
		}
	}
	return nil, ErrUnavailable
}

func applyRuntimeOptions(st *middleware.State, m model.Model) error {
	if st == nil {
		return fmt.Errorf("modelrouter: runtime options require state")
	}
	if m == nil {
		return fmt.Errorf("modelrouter: runtime options require a model")
	}

	thinking, err := thinkingEnabled(st)
	if err != nil {
		return err
	}
	effort, err := reasoningEffort(st)
	if err != nil {
		return err
	}

	info := m.Info()
	st.SetValue(ValueSupportsVision, info.SupportsVision)
	if st.ModelInput == nil {
		return nil
	}

	req := *st.ModelInput
	req.Thinking = thinking && info.SupportsThinking
	req.ExtraBody = cloneExtraBody(req.ExtraBody)
	delete(req.ExtraBody, ValueReasoningEffort)
	if effort != "" && info.SupportsReasoningEffort {
		req.ExtraBody[ValueReasoningEffort] = effort
	}
	if len(req.ExtraBody) == 0 {
		req.ExtraBody = nil
	}
	st.ModelInput = &req
	return nil
}

func thinkingEnabled(st *middleware.State) (bool, error) {
	value, ok := st.Value(ValueThinkingEnabled)
	if !ok || value == nil {
		// Match the Python harness: thinking is enabled by default and then
		// constrained by the selected model's declared capability.
		return true, nil
	}
	enabled, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("modelrouter: %s must be a boolean", ValueThinkingEnabled)
	}
	return enabled, nil
}

func reasoningEffort(st *middleware.State) (string, error) {
	value, ok := st.Value(ValueReasoningEffort)
	if !ok || value == nil {
		return "", nil
	}
	effort, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("modelrouter: %s must be a string", ValueReasoningEffort)
	}
	return strings.TrimSpace(effort), nil
}

func cloneExtraBody(in map[string]any) map[string]any {
	if len(in) == 0 {
		return make(map[string]any)
	}
	out := make(map[string]any, len(in)+1)
	for key, value := range in {
		out[key] = value
	}
	return out
}
