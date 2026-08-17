package modelrouter_test

import (
	"context"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/modelrouter"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
)

func TestRuntimeOptionsUseSelectedNamedModelCapabilities(t *testing.T) {
	t.Parallel()

	standard := faux.New(faux.Text("standard")).WithInfo(model.Info{
		Name:                    "standard",
		SupportsThinking:        false,
		SupportsReasoningEffort: false,
		SupportsVision:          false,
	})
	selected := faux.New(faux.Text("selected")).WithInfo(model.Info{
		Name:                    "selected",
		SupportsThinking:        true,
		SupportsReasoningEffort: true,
		SupportsVision:          true,
	})
	r := mustRouter(t, modelrouter.Config{
		Models:      map[modelrouter.Tier]model.Model{modelrouter.TierStandard: standard},
		NamedModels: map[string]model.Model{"selected": selected},
	})
	st := runtimeOptionsState(map[string]any{
		modelrouter.ValueModelName:       "selected",
		modelrouter.ValueThinkingEnabled: true,
		modelrouter.ValueReasoningEffort: "high",
	})
	st.ModelInput.ExtraBody = map[string]any{"existing": "value"}

	options := r.RuntimeOptions()
	if err := options.BeforeAgent(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if supported, _ := st.Value(modelrouter.ValueSupportsVision); supported != true {
		t.Fatalf("supports_vision = %#v, want true", supported)
	}
	if err := options.BeforeModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if !st.ModelInput.Thinking {
		t.Fatal("thinking was not enabled for a capable selected model")
	}
	if got := st.ModelInput.ExtraBody[modelrouter.ValueReasoningEffort]; got != "high" {
		t.Fatalf("reasoning_effort = %#v, want high", got)
	}
	if got := st.ModelInput.ExtraBody["existing"]; got != "value" {
		t.Fatalf("existing extra body value = %#v", got)
	}

	if _, _, err := r.Sample(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	got, ok := selected.LastRequest()
	if !ok {
		t.Fatal("selected model did not receive a request")
	}
	if !got.Thinking || got.ExtraBody[modelrouter.ValueReasoningEffort] != "high" {
		t.Fatalf("selected model request = %#v", got)
	}
	if standard.CallCount() != 0 {
		t.Fatal("standard model was called for an explicit named selection")
	}
}

func TestRuntimeOptionsDisableUnsupportedSelectedModelFeatures(t *testing.T) {
	t.Parallel()

	standard := faux.New(faux.Text("standard")).WithInfo(model.Info{
		Name:                    "standard",
		SupportsThinking:        true,
		SupportsReasoningEffort: true,
		SupportsVision:          true,
	})
	selected := faux.New(faux.Text("selected")).WithInfo(model.Info{Name: "selected"})
	r := mustRouter(t, modelrouter.Config{
		Models:      map[modelrouter.Tier]model.Model{modelrouter.TierStandard: standard},
		NamedModels: map[string]model.Model{"selected": selected},
	})
	st := runtimeOptionsState(map[string]any{
		modelrouter.ValueModelName:       "selected",
		modelrouter.ValueThinkingEnabled: true,
		modelrouter.ValueReasoningEffort: "high",
	})
	st.ModelInput.Thinking = true
	st.ModelInput.ExtraBody = map[string]any{
		modelrouter.ValueReasoningEffort: "stale",
		"existing":                       "value",
	}

	if err := r.RuntimeOptions().BeforeModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if st.ModelInput.Thinking {
		t.Fatal("thinking remained enabled for an unsupported selected model")
	}
	if _, ok := st.ModelInput.ExtraBody[modelrouter.ValueReasoningEffort]; ok {
		t.Fatal("reasoning_effort remained set for an unsupported selected model")
	}
	if got := st.ModelInput.ExtraBody["existing"]; got != "value" {
		t.Fatalf("existing extra body value = %#v", got)
	}
	if supported, _ := st.Value(modelrouter.ValueSupportsVision); supported != false {
		t.Fatalf("supports_vision = %#v, want false", supported)
	}
}

func TestRuntimeOptionsResolveVisionTierBeforeModelInputExists(t *testing.T) {
	t.Parallel()

	standard := faux.New(faux.Text("standard")).WithInfo(model.Info{Name: "standard"})
	vision := faux.New(faux.Text("vision")).WithInfo(model.Info{
		Name:           "vision",
		SupportsVision: true,
	})
	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{
		modelrouter.TierStandard: standard,
		modelrouter.TierVision:   vision,
	}})
	st := runtimeOptionsState(nil)
	st.History.Append(message.Message{
		Role:          message.RoleUser,
		ContentBlocks: []message.ContentBlock{{Type: "image", MimeType: "image/png", Data: "AAAA"}},
	})
	st.ModelInput = nil

	if err := r.RuntimeOptions().BeforeAgent(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if supported, _ := st.Value(modelrouter.ValueSupportsVision); supported != true {
		t.Fatalf("supports_vision = %#v, want true", supported)
	}
}

func TestSampleReappliesOptionsWhenFallbackChangesCapabilities(t *testing.T) {
	t.Parallel()

	standard := faux.New(faux.Fail(model.ErrProviderUnavailable)).WithInfo(model.Info{
		Name:                    "standard",
		SupportsThinking:        true,
		SupportsReasoningEffort: true,
		SupportsVision:          true,
	})
	fast := faux.New(faux.Text("fallback")).WithInfo(model.Info{Name: "fast"})
	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{
		modelrouter.TierStandard: standard,
		modelrouter.TierFast:     fast,
	}})
	st := runtimeOptionsState(map[string]any{
		modelrouter.ValueThinkingEnabled: true,
		modelrouter.ValueReasoningEffort: "medium",
	})

	if _, _, err := r.Sample(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	standardRequest, ok := standard.LastRequest()
	if !ok || !standardRequest.Thinking || standardRequest.ExtraBody[modelrouter.ValueReasoningEffort] != "medium" {
		t.Fatalf("standard request = %#v", standardRequest)
	}
	fastRequest, ok := fast.LastRequest()
	if !ok {
		t.Fatal("fallback model did not receive a request")
	}
	if fastRequest.Thinking {
		t.Fatal("fallback inherited unsupported thinking")
	}
	if _, ok := fastRequest.ExtraBody[modelrouter.ValueReasoningEffort]; ok {
		t.Fatal("fallback inherited unsupported reasoning_effort")
	}
	if supported, _ := st.Value(modelrouter.ValueSupportsVision); supported != false {
		t.Fatalf("final supports_vision = %#v, want false", supported)
	}
}

func TestRuntimeOptionsValidateRunValueTypes(t *testing.T) {
	t.Parallel()

	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{
		modelrouter.TierStandard: faux.New(faux.Text("unused")),
	}})
	st := runtimeOptionsState(map[string]any{modelrouter.ValueThinkingEnabled: "yes"})
	if err := r.RuntimeOptions().BeforeModel(context.Background(), st); err == nil {
		t.Fatal("non-boolean thinking_enabled was accepted")
	}

	st = runtimeOptionsState(map[string]any{modelrouter.ValueReasoningEffort: true})
	if err := r.RuntimeOptions().BeforeModel(context.Background(), st); err == nil {
		t.Fatal("non-string reasoning_effort was accepted")
	}
}

func TestRuntimeOptionsDefaultThinkingMatchesPythonHarness(t *testing.T) {
	t.Parallel()

	capable := faux.New(faux.Text("ok")).WithInfo(model.Info{
		Name:             "capable",
		SupportsThinking: true,
	})
	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{
		modelrouter.TierStandard: capable,
	}})
	st := runtimeOptionsState(nil)
	if err := r.RuntimeOptions().BeforeModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if !st.ModelInput.Thinking {
		t.Fatal("thinking defaulted to false for a capable model")
	}
}

func runtimeOptionsState(values map[string]any) *lifecycle.State {
	h := message.NewHistory()
	h.Append(message.Message{Role: message.RoleUser, Content: "hi"})
	st := lifecycle.NewState(lifecycle.StateInit{History: h})
	for key, value := range values {
		st.SetValue(key, value)
	}
	st.ModelInput = &model.Request{Messages: h.All()}
	return st
}
