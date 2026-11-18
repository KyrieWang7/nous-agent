package service

import (
	"context"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/plugincontrol"
)

func (s *Service) Plugins(ctx context.Context) (plugincontrol.Snapshot, error) {
	if s.pluginsCtl == nil {
		return plugincontrol.Snapshot{}, plugincontrol.ErrUnavailable
	}
	return s.pluginsCtl.Snapshot(ctx)
}
func (s *Service) ChangePlugins(ctx context.Context, change plugincontrol.Change) (plugincontrol.Snapshot, error) {
	if s.pluginsCtl == nil {
		return plugincontrol.Snapshot{}, plugincontrol.ErrUnavailable
	}
	return s.pluginsCtl.Apply(ctx, change)
}
