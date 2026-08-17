// Package harness 是装配门面：把 Options 变成一个装好的 loop.Runner。
//
// 这里是唯一知道"默认怎么接线"的地方。内核只认接口，具体实现由这里挑选。
// 本包只接受 typed Options，不认识 YAML —— 配置加载归 pkg/config（应用层）。
package harness

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/assembly"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	runtimeplugin "github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/plugin"
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

	// LifecycleHandlers 是内核生命周期处理器。Extensions 按声明锚点插入。
	LifecycleHandlers []lifecycle.Handler
	Extensions        []lifecycle.Handler

	// DeclaredHandlers 列出配置声明启用的生命周期处理器。装配时校验它们
	// 确实出现在最终链里 —— 这条断言存在的唯一目的是让"声明了但从未生效"
	// 在启动时暴露（设计文档 §4.3）。
	DeclaredHandlers []string

	Limits           loop.Limits
	LifecycleTimeout time.Duration
	ToolConcurrency  int
	TokenLimit       int

	// 以下可为 nil，内核退化为不做该步骤。
	Compactor loop.Compactor
	Trimmer   loop.Trimmer
	ToolSet   loop.ToolSetResolver
	Publisher loop.Publisher
	StopGate  loop.StopGate
	Executor  loop.ToolExecutor

	Logger *slog.Logger

	// Capabilities and Plugins are the runtime assembly boundary. Model, Tools,
	// and Registry are typed construction inputs registered into the same
	// immutable capability generation used to build the Kernel views.
	Capabilities *capability.Registry
	// CapabilityValues adapts already-constructed sandbox, memory, skill, MCP
	// or agent providers into the unified capability boundary. Providers that
	// need run-scoped construction should be registered as capability.Entry on
	// Capabilities directly.
	CapabilityValues []capability.Value
	Plugins          []runtimeplugin.Plugin
	GenerationID     string
}

// Harness 是装配好的运行时。
type Harness struct {
	runner    *loop.Runner
	lifecycle *lifecycle.Dispatcher
	runtime   *assembly.Runtime
}

// New 校验并装配 Harness。
//
// 全部校验在这里一次做完并 fail fast：配置引用了不存在的东西、
// 声明了却没进入生命周期序列的 handler，都必须让进程起不来，而不是上线后
// 才发现某个治理 handler 一直没跑。
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

	capabilities := opts.Capabilities
	if capabilities == nil {
		capabilities = capability.NewRegistry()
	}
	for _, value := range opts.CapabilityValues {
		if capabilities.Has(value.Name) {
			continue
		}
		if err := capability.RegisterValue(capabilities, value); err != nil {
			return nil, fmt.Errorf("harness: registering capability %q: %w", value.Name, err)
		}
	}
	if opts.Model != nil && !capabilities.Has("model.default") {
		defaultModel := opts.Model
		if err := capabilities.Register(capability.Entry{
			Definition: capability.Definition{Name: "model.default", Kind: capability.KindModel, Description: defaultModel.Info().Name, Scope: capability.ScopeGlobal},
			Resolver:   func(context.Context, capability.ResolveRequest) (any, error) { return defaultModel, nil },
		}); err != nil {
			return nil, fmt.Errorf("harness: registering default model capability: %w", err)
		}
	}
	generationID := opts.GenerationID
	if generationID == "" {
		generationID = fmt.Sprintf("harness-%d", time.Now().UTC().UnixNano())
	}
	plugins := append([]runtimeplugin.Plugin(nil), opts.Plugins...)
	plugins = append(plugins, newToolCatalogPlugin(registry, opts.Plugins))
	runtimeAssembly, err := assembly.New(context.Background(), assembly.Options{
		GenerationID: generationID,
		Capabilities: capabilities,
		Plugins:      plugins,
		Tools:        registry,
	})
	if err != nil {
		return nil, fmt.Errorf("harness: assembling runtime: %w", err)
	}

	handlers, err := lifecycle.Compose(opts.LifecycleHandlers, opts.Extensions)
	if err != nil {
		_ = runtimeAssembly.Close(context.Background())
		return nil, fmt.Errorf("harness: composing lifecycle handlers: %w", err)
	}
	if err := lifecycle.VerifyPresent(handlers, opts.DeclaredHandlers); err != nil {
		_ = runtimeAssembly.Close(context.Background())
		return nil, fmt.Errorf("harness: %w", err)
	}

	dispatcher, err := lifecycle.NewDispatcher(handlers, lifecycle.DispatcherOptions{
		Timeout: opts.LifecycleTimeout,
		Logger:  opts.Logger,
	})
	if err != nil {
		_ = runtimeAssembly.Close(context.Background())
		return nil, fmt.Errorf("harness: building lifecycle dispatcher: %w", err)
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
		Lifecycle: dispatcher,
		Limits:    opts.Limits,
		Compactor: opts.Compactor,
		Trimmer:   trimmer,
		ToolSet:   opts.ToolSet,
		Publisher: opts.Publisher,
		StopGate:  opts.StopGate,
	})
	if err != nil {
		_ = runtimeAssembly.Close(context.Background())
		return nil, fmt.Errorf("harness: %w", err)
	}

	return &Harness{runner: runner, lifecycle: dispatcher, runtime: runtimeAssembly}, nil
}

type toolCatalogPlugin struct {
	registry     *tool.Registry
	dependencies []string
}

func newToolCatalogPlugin(registry *tool.Registry, plugins []runtimeplugin.Plugin) *toolCatalogPlugin {
	dependencies := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		if plugin != nil {
			dependencies = append(dependencies, plugin.Name())
		}
	}
	return &toolCatalogPlugin{registry: registry, dependencies: dependencies}
}

func (*toolCatalogPlugin) Name() string { return "harness.tool-catalog" }
func (p *toolCatalogPlugin) Dependencies() []string {
	return append([]string(nil), p.dependencies...)
}
func (p *toolCatalogPlugin) Start(_ context.Context, host runtimeplugin.Host) error {
	for _, name := range p.registry.Names() {
		capabilityName := "tool." + name
		if host.Capabilities.Has(capabilityName) {
			continue
		}
		definition, err := p.registry.Get(name)
		if err != nil {
			return err
		}
		toolDefinition := definition
		if err := host.Capabilities.Register(capability.Entry{
			Definition: capability.Definition{Name: capabilityName, Kind: capability.KindTool, Description: toolDefinition.Description, Scope: capability.ScopeRun},
			Resolver:   func(context.Context, capability.ResolveRequest) (any, error) { return toolDefinition, nil },
		}); err != nil {
			return err
		}
	}
	return nil
}
func (*toolCatalogPlugin) Stop(context.Context) error { return nil }

// Runner 返回内核。
func (h *Harness) Runner() *loop.Runner { return h.runner }

// Lifecycle 返回内核的固定生命周期分发器。
func (h *Harness) Lifecycle() *lifecycle.Dispatcher { return h.lifecycle }

// Runtime returns the assembled capability/plugin generation.
func (h *Harness) Runtime() *assembly.Runtime {
	if h == nil {
		return nil
	}
	return h.runtime
}

// ResolveCapability resolves a capability from the immutable generation used
// by this harness. Business layers can use this method without reaching into
// the mutable assembly registry or Kernel-specific typed views.
func (h *Harness) ResolveCapability(ctx context.Context, kind capability.Kind, name string, req capability.ResolveRequest) (any, error) {
	if h == nil || h.runtime == nil || h.runtime.Generation == nil {
		return nil, errors.New("harness: runtime is not initialized")
	}
	if req.GenerationID == "" {
		req.GenerationID = h.runtime.Generation.ID()
	}
	return h.runtime.Generation.Capabilities().Resolve(ctx, name, kind, req)
}

// Close releases runtime plugin resources. Existing runner behavior is
// unchanged; callers that do not use plugins can continue to omit Close.
func (h *Harness) Close() error {
	if h == nil || h.runtime == nil {
		return nil
	}
	return h.runtime.Close(context.Background())
}
