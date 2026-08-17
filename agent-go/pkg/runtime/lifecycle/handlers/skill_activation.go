package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/skill"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

const NameSkillActivation = "skillActivation"
const skillMarkerPrefix = "[Skill activated: "

type SkillActivation struct {
	registryCapability string
	tools              *tool.Registry
	force              []string
}

func NewSkillActivation(registryCapability string, tools *tool.Registry, force []string) *SkillActivation {
	return &SkillActivation{registryCapability: registryCapability, tools: tools, force: append([]string(nil), force...)}
}
func (s *SkillActivation) Name() string { return NameSkillActivation }
func (s *SkillActivation) BeforeModel(ctx context.Context, st *lifecycle.State) error {
	if s.tools == nil || st.History == nil {
		return nil
	}
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || !run.Capabilities.Initialized() {
		return errors.New("skill activation: capability view is not initialized")
	}
	registry, err := capability.ResolveViewAs[*skill.Registry](ctx, run.Capabilities, s.registryCapability, capability.KindSkill, capability.ResolveRequest{
		GenerationID: run.GenerationID, ThreadID: run.ThreadID, RunID: run.RunID, Values: run.Values,
	})
	if err != nil {
		return fmt.Errorf("skill activation: resolving registry capability: %w", err)
	}
	active, err := registry.Match(skill.ActivationContext{Prompt: lastUserPrompt(st.History.All()), ForceSkills: s.force})
	if err != nil {
		return err
	}
	if len(active) == 0 {
		return nil
	}
	registered := map[string]bool{}
	deferred := map[string]bool{}
	for _, name := range s.tools.Names() {
		registered[name] = true
		d, _ := s.tools.Get(name)
		deferred[name] = d.Deferred
	}
	allow, disclosed := skill.NarrowTools(st.ToolSet, active, registered, deferred)
	st.ToolSet = allow
	st.DisclosedTools = disclosed
	if st.ModelInput != nil {
		st.ModelInput.Tools = s.tools.Schemas(allow, disclosed...)
	}
	for _, sk := range active {
		st.ActivatedSkills = appendUnique(st.ActivatedSkills, sk.Name)
		if alreadyActivated(st.History.All(), sk.Name) {
			continue
		}
		if err := publishRuntime(ctx, st, runtime.EventSkillActivated, map[string]any{"skill": sk.Name}); err != nil {
			return err
		}
		body, err := sk.Body()
		if err != nil {
			return err
		}
		marker := skillMarkerPrefix + sk.Name + "]"
		msg := message.Message{Role: message.RoleSystem, Content: fmt.Sprintf("%s\n\n%s", marker, body)}
		st.History.Append(msg)
		if st.ModelInput != nil {
			st.ModelInput.Messages = append(st.ModelInput.Messages, msg)
		}
	}
	return nil
}
func lastUserPrompt(msgs []message.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == message.RoleUser {
			return msgs[i].Content
		}
	}
	return ""
}
func alreadyActivated(msgs []message.Message, name string) bool {
	marker := skillMarkerPrefix + name + "]"
	for _, m := range msgs {
		if strings.Contains(m.Content, marker) {
			return true
		}
	}
	return false
}
func appendUnique(in []string, v string) []string {
	for _, x := range in {
		if x == v {
			return in
		}
	}
	return append(in, v)
}

var _ lifecycle.BeforeModel = (*SkillActivation)(nil)
