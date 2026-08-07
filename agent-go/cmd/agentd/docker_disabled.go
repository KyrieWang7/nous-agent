//go:build !docker

package main

import (
	"errors"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/config"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
)

func newDockerSandbox(config.SandboxConfig) (sandbox.Provider, error) {
	return nil, errors.New("agentd: docker sandbox requires building with -tags=docker")
}
