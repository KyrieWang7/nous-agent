// Package harness 是装配门面：把 Options 变成一个装好的 loop.Runner。
//
// 这里是唯一知道"默认怎么接线"的地方。内核只认接口，具体实现由这里挑选。
// 本包只接受 typed Options，不认识 YAML —— 配置加载归 pkg/config（应用层）。
package harness

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// Options 是装配一个 Runner 所需的一切。
type Options struct {
	// Model 是默认模型。与 Sampler 二选一：给了 Sampler 就用它，
	// 否则用 Model 构造一个 DirectSampler。
	Model   model.Model
	Sampler loop.Sampler

	// Tools 是要注册的工具。也可以直接给 Registry。
	Tools    []tool.Definition
	Registry *tool.Registry

	// Middleware 是基础链。Extras 按声明的锚点插入其中。
	Middleware []middleware.Middleware
	Extras     []middleware.Middleware

	// DeclaredMiddleware 列出配置声明启用的中间件名。装配时校验它们
	// 确实出现在最终链里 —— 这条断言存在的唯一目的是让"声明了但从未生效"
	// 在启动时暴露（设计文档 §4.3）。
	DeclaredMiddleware []string

	Limits            loop.Limits
	MiddlewareTimeout time.Duration
	ToolConcurrency   int
	TokenLimit        int

	// 以下可为 nil，内核退化为不做该步骤。
	Compactor loop.Compactor
	Trimmer   loop.Trimmer
	ToolSet   loop.ToolSetResolver
	Publisher loop.Publisher
	StopGate  loop.StopGate
	Executor  loop.ToolExecutor

	Logger *slog.Logger
}

// Harness 是装配好的运行时。
type Harness struct {
	runner *loop.Runner
	chain  *middleware.Chain
}

// New 校验并装配 Harness。
//
// 全部校验在这里一次做完并 fail fast：配置引用了不存在的东西、
// 声明了却没进链的中间件，都必须让进程起不来，而不是上线后才发现某个
// 治理中间件一直没跑。
func New(opts Options) (*Harness, error) {
	if opts.Model == nil && opts.Sampler == nil {
		return nil, errors.New("harness: options require a Model or a Sampler")
	}

	registry := opts.Registry
	if registry == nil {
		registry = tool.NewRegistry()
	}
	if err := registry.RegisterAll(opts.Tools...); err != nil {
		return nil, fmt.Errorf("harness: registering tools: %w", err)
	}

	chainMWs, err := middleware.Build(opts.Middleware, opts.Extras)
	if err != nil {
		return nil, fmt.Errorf("harness: assembling middleware chain: %w", err)
	}
	if err := middleware.VerifyPresent(chainMWs, opts.DeclaredMiddleware); err != nil {
		return nil, fmt.Errorf("harness: %w", err)
	}

	chain, err := middleware.NewChain(chainMWs, middleware.ChainOptions{
		Timeout: opts.MiddlewareTimeout,
		Logger:  opts.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("harness: building middleware chain: %w", err)
	}

	sampler := opts.Sampler
	if sampler == nil {
		sampler = loop.NewDirectSampler(opts.Model)
	}

	executor := opts.Executor
	if executor == nil {
		executor = tool.NewExecutor(registry, tool.ExecutorOptions{Concurrency: opts.ToolConcurrency})
	}

	trimmer := opts.Trimmer
	if trimmer == nil && opts.TokenLimit > 0 {
		trimmer = message.NewTrimmer(opts.TokenLimit, nil)
	}

	runner, err := loop.NewRunner(loop.Config{
		Sampler:   sampler,
		Registry:  registry,
		Executor:  executor,
		Chain:     chain,
		Limits:    opts.Limits,
		Compactor: opts.Compactor,
		Trimmer:   trimmer,
		ToolSet:   opts.ToolSet,
		Publisher: opts.Publisher,
		StopGate:  opts.StopGate,
	})
	if err != nil {
		return nil, fmt.Errorf("harness: %w", err)
	}

	return &Harness{runner: runner, chain: chain}, nil
}

// Runner 返回内核。
func (h *Harness) Runner() *loop.Runner { return h.runner }

// Chain 返回中间件链。
func (h *Harness) Chain() *middleware.Chain { return h.chain }
