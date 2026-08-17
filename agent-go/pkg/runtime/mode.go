package runtime

import (
	"fmt"
	"strings"
)

// Mode is the user-facing execution intent normalized by the Harness.
// Product transports may expose different labels, but Kernel behavior is
// driven by this stable runtime policy rather than by UI booleans.
type Mode string

const (
	ModeFlash    Mode = "flash"
	ModeThinking Mode = "thinking"
	ModePro      Mode = "pro"
	ModeUltra    Mode = "ultra"
)

// ModePolicy is the canonical execution policy for one run.
type ModePolicy struct {
	Mode            Mode
	ThinkingEnabled bool
	PlanMode        bool
	SubagentEnabled bool
	SwarmEnabled    bool
	ReasoningEffort string
}

// ResolveModePolicy normalizes a run's product intent into Kernel policy. A
// missing mode uses the canonical default; legacy derived booleans are never
// read back as policy input.
func ResolveModePolicy(values map[string]any) (ModePolicy, error) {
	modeValue, ok := values["mode"]
	if !ok || modeValue == nil || strings.TrimSpace(fmt.Sprint(modeValue)) == "" {
		modeValue = string(ModePro)
	}

	mode := Mode(strings.ToLower(strings.TrimSpace(fmt.Sprint(modeValue))))
	policy := ModePolicy{Mode: mode}
	switch mode {
	case ModeFlash:
		policy.ReasoningEffort = ""
	case ModeThinking:
		policy.ThinkingEnabled, policy.ReasoningEffort = true, "low"
	case ModePro:
		policy.ThinkingEnabled, policy.PlanMode, policy.ReasoningEffort = true, true, "medium"
	case ModeUltra:
		policy.ThinkingEnabled, policy.PlanMode, policy.SubagentEnabled, policy.ReasoningEffort = true, true, true, "high"
	default:
		return ModePolicy{}, fmt.Errorf("runtime: unsupported mode %q", mode)
	}
	var err error
	policy.SwarmEnabled, err = optionalBool(values, "swarm_enabled", false)
	if err != nil {
		return ModePolicy{}, err
	}
	if policy.SwarmEnabled && mode != ModeUltra {
		return ModePolicy{}, fmt.Errorf("runtime: swarm requires mode %q", ModeUltra)
	}
	return policy, nil
}

func optionalBool(values map[string]any, key string, fallback bool) (bool, error) {
	v, ok := values[key]
	if !ok || v == nil {
		return fallback, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("runtime: %s must be a boolean", key)
	}
	return b, nil
}
