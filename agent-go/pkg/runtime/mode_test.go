package runtime

import "testing"

func TestResolveModePolicy(t *testing.T) {
	tests := []struct {
		mode                     Mode
		plan, subagent, thinking bool
		effort                   string
	}{
		{ModeFlash, false, false, false, ""},
		{ModeThinking, false, false, true, "low"},
		{ModePro, true, false, true, "medium"},
		{ModeUltra, true, true, true, "high"},
	}
	for _, tt := range tests {
		got, err := ResolveModePolicy(map[string]any{"mode": string(tt.mode)})
		if err != nil {
			t.Fatalf("mode %s: %v", tt.mode, err)
		}
		if got.Mode != tt.mode || got.PlanMode != tt.plan || got.SubagentEnabled != tt.subagent || got.ThinkingEnabled != tt.thinking || got.ReasoningEffort != tt.effort {
			t.Fatalf("mode %s: got %#v", tt.mode, got)
		}
	}
}

func TestResolveModePolicyUsesCanonicalDefaultAndIgnoresDerivedInputs(t *testing.T) {
	got, err := ResolveModePolicy(map[string]any{
		"thinking_enabled": false,
		"is_plan_mode":     false,
		"subagent_enabled": true,
		"reasoning_effort": "custom",
	})
	if err != nil || got.Mode != ModePro || !got.ThinkingEnabled || !got.PlanMode || got.SubagentEnabled || got.ReasoningEffort != "medium" {
		t.Fatalf("got %#v, err %v", got, err)
	}
}

func TestResolveModePolicyRequiresUltraForSwarm(t *testing.T) {
	if _, err := ResolveModePolicy(map[string]any{"mode": "pro", "swarm_enabled": true}); err == nil {
		t.Fatal("swarm was accepted outside ultra mode")
	}
	got, err := ResolveModePolicy(map[string]any{"mode": "ultra", "swarm_enabled": true})
	if err != nil || !got.SwarmEnabled || !got.SubagentEnabled {
		t.Fatalf("got %#v, err %v", got, err)
	}
}

func TestResolveModePolicyRejectsMalformedSwarmIntent(t *testing.T) {
	if _, err := ResolveModePolicy(map[string]any{"mode": "ultra", "swarm_enabled": "true"}); err == nil {
		t.Fatal("non-boolean swarm intent was accepted")
	}
}

func TestResolveModePolicyRejectsUnknownMode(t *testing.T) {
	if _, err := ResolveModePolicy(map[string]any{"mode": "experimental"}); err == nil {
		t.Fatal("unknown mode was accepted")
	}
}
