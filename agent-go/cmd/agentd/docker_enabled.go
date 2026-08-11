//go:build docker

package main

import (
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/config"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
	dockersandbox "github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox/docker"
)

func newDockerSandbox(cfg config.SandboxConfig) (sandbox.Provider, error) {
	return dockersandbox.NewProvider(dockersandbox.Options{BaseDir: cfg.BaseDir, VirtualRoot: sandboxVirtualRoot, ExecTimeout: cfg.ExecTimeout}), nil
}
