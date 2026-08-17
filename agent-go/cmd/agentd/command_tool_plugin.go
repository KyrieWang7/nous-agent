package main

import (
	"context"
	"errors"
	"sort"

	commandplugin "github.com/KyrieWang7/nous-agent/agent-go/pkg/plugin"
	runtimeplugin "github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/plugin"
)

type commandToolPlugin struct {
	name          string
	contributions []commandplugin.Contribution
}

func commandToolPlugins(contributions []commandplugin.Contribution) []runtimeplugin.Plugin {
	grouped := make(map[string][]commandplugin.Contribution)
	for _, contribution := range contributions {
		grouped[contribution.PluginName] = append(grouped[contribution.PluginName], contribution)
	}
	names := make([]string, 0, len(grouped))
	for name := range grouped {
		names = append(names, name)
	}
	sort.Strings(names)
	plugins := make([]runtimeplugin.Plugin, 0, len(names))
	for _, name := range names {
		plugins = append(plugins, &commandToolPlugin{name: name, contributions: grouped[name]})
	}
	return plugins
}

func (p *commandToolPlugin) Name() string             { return "command-tools." + p.name }
func (*commandToolPlugin) Dependencies() []string     { return nil }
func (*commandToolPlugin) Stop(context.Context) error { return nil }
func (p *commandToolPlugin) Start(_ context.Context, host runtimeplugin.Host) error {
	if host.Tools == nil {
		return errors.New("command tool plugin requires a tool registry")
	}
	for _, contribution := range p.contributions {
		if err := host.Tools.Register(contribution.Definition); err != nil {
			return err
		}
	}
	return nil
}
