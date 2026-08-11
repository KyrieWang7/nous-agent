package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/langgraphapi"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/acp"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/compaction"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/config"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/guardrail"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/harness"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/hooks"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/mcp"
	mem "github.com/KyrieWang7/nous-agent/agent-go/pkg/memory"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	mw "github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware/builtin"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/anthropic"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/openai"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/modelrouter"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/permission"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/plugin"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/prompt"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox/local"
	remotesandbox "github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox/remote"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/skill"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/subagent"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/swarm"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/telemetry"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool/builtin"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool/community"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const (
	sandboxVirtualRoot  = "/mnt/user-data"
	delegatedTaskPrompt = `

<delegated_task>
You are a delegated subagent. Complete the assigned prompt directly, do not
create nested tasks, and return concrete findings to the team lead. When Swarm
messaging tools are available, use send_message with to="team-lead" to report
or coordinate, and do not change team lifecycle.
</delegated_task>`
)

func main() {
	if err := run(); err != nil {
		slog.Error("agentd stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "config.yaml", "path to YAML configuration")
	flag.Parse()
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	apiOpts := langgraphapi.Options{HeartbeatInterval: cfg.Runtime.HeartbeatInterval, Pricer: buildPricer(cfg.Models)}
	apiOpts.Bus = runtime.NewMemoryBus(runtime.BusOptions{
		RingCapacity:     cfg.Runtime.EventBufferSize,
		SubscriberBuffer: cfg.Runtime.EventBufferSize,
	})
	var taskStore subagent.TaskStore
	var pool *pgxpool.Pool
	var postgresEvents runtime.EventStore
	if cfg.Runtime.DatabaseURL != "" {
		pool, err = openPool(cfg.Runtime.DatabaseURL)
		if err != nil {
			return err
		}
		defer pool.Close()
		apiOpts.Store, err = langgraphapi.NewPostgresStore(pool)
		if err != nil {
			return err
		}
		postgresEvents, err = runtime.NewPostgresEventStore(pool)
		if err != nil {
			return err
		}
		apiOpts.EventStore = postgresEvents
	}
	if cfg.Runtime.RedisURL != "" {
		redisOpts, parseErr := redis.ParseURL(cfg.Runtime.RedisURL)
		if parseErr != nil {
			return fmt.Errorf("agentd: parsing redis URL: %w", parseErr)
		}
		client := redis.NewClient(redisOpts)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := client.Ping(ctx).Err(); err != nil {
			cancel()
			_ = client.Close()
			return fmt.Errorf("agentd: pinging redis: %w", err)
		}
		cancel()
		defer func() { _ = client.Close() }()
		taskStore = subagent.NewRedisTaskStore(client)
		redisEvents := runtime.NewRedisEventStore(client, cfg.Runtime.EventTTL)
		apiOpts.EventStore = redisEvents
		if postgresEvents != nil {
			apiOpts.EventStore = runtime.MultiEventStore{Primary: redisEvents, Mirrors: []runtime.EventStore{postgresEvents}}
		}
		apiOpts.Registry, err = runtime.NewRegistry(context.Background(), runtime.RegistryOptions{Backend: runtime.NewRedisRegistryBackend(client), Owner: cfg.Server.Address, TTL: cfg.Runtime.EventTTL})
		if err != nil {
			return err
		}
	}
	if apiOpts.Registry != nil {
		apiOpts.OwnRegistry = true
	}
	built, err := buildAgent(cfg, taskStore, pool)
	if err != nil {
		return err
	}
	reloadable, err := newReloadableAgent(*configPath, cfg, built, func(reloaded config.Config) (builtAgent, error) {
		return buildAgent(reloaded, taskStore, pool)
	})
	if err != nil {
		built.Close()
		return err
	}
	defer reloadable.Close()
	apiOpts.Agent = reloadable
	apiOpts.AllowedTools = built.tools
	api, err := langgraphapi.New(apiOpts)
	if err != nil {
		return err
	}
	defer api.Close()

	server := &http.Server{Addr: cfg.Server.Address, Handler: api, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx) //nolint:contextcheck // shutdown must outlive the cancelled signal context
	}()
	slog.Info("Nous agent-go listening", "address", cfg.Server.Address, "native_api", "/api/v1", "compat_api", "/threads")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func openPool(databaseURL string) (*pgxpool.Pool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("agentd: opening postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("agentd: pinging postgres: %w", err)
	}
	return pool, nil
}

type builtAgent struct {
	agent  langgraphapi.Agent
	tools  []string
	pricer *runtime.Pricer
	close  []func() error
	chain  *middleware.Chain
}

func (b builtAgent) Close() {
	for i := len(b.close) - 1; i >= 0; i-- {
		if err := b.close[i](); err != nil {
			slog.Warn("closing agent dependency", "error", err)
		}
	}
}

func buildAgent(cfg config.Config, taskStore subagent.TaskStore, pool *pgxpool.Pool) (builtAgent, error) {
	mc, err := cfg.SelectedModel()
	if err != nil {
		return builtAgent{}, err
	}
	namedModels := make(map[string]model.Model, len(cfg.Models))
	for _, modelConfig := range cfg.Models {
		configured, modelErr := buildModel(modelConfig)
		if modelErr != nil {
			return builtAgent{}, modelErr
		}
		namedModels[modelConfig.Name] = configured
	}
	m := namedModels[mc.Name]
	if m == nil {
		return builtAgent{}, fmt.Errorf("agentd: default model %q was not built", mc.Name)
	}

	var compactor loop.Compactor
	if cfg.Summarization.Enabled {
		trigger := cfg.Summarization.TriggerTokens
		if trigger <= 0 {
			trigger = mc.ContextLength * 3 / 4
		}
		if trigger <= 0 {
			trigger = 96_000
		}
		compactor, err = compaction.New(compaction.Config{
			TriggerTokens:    trigger,
			KeepMessages:     cfg.Summarization.KeepMessages,
			MaxSummaryTokens: cfg.Summarization.MaxSummaryTokens,
			MaxInputMessages: cfg.Summarization.MaxInputMessages,
			MaxInputChars:    cfg.Summarization.MaxInputChars,
		}, compaction.NewModelSummariser(runtime.InstrumentModel(m, runtime.BucketMiddleware, "compaction")))
		if err != nil {
			return builtAgent{}, err
		}
	}
	router, err := modelrouter.New(modelrouter.Config{
		Models:        map[modelrouter.Tier]model.Model{modelrouter.TierStandard: m},
		NamedModels:   namedModels,
		Stream:        true,
		Compactor:     compactor,
		OnStreamEvent: publishModelStreamEvent,
	})
	if err != nil {
		return builtAgent{}, err
	}

	registry := tool.NewRegistry()
	var closers []func() error
	var sandboxProvider sandbox.Provider
	if cfg.Sandbox.Enabled {
		switch cfg.Sandbox.Provider {
		case "local":
			sandboxProvider = local.NewProvider(local.Options{BaseDir: cfg.Sandbox.BaseDir, VirtualRoot: sandboxVirtualRoot, ExecTimeout: cfg.Sandbox.ExecTimeout})
		case "docker":
			sandboxProvider, err = newDockerSandbox(cfg.Sandbox)
			if err != nil {
				return builtAgent{}, err
			}
		case "remote":
			sandboxProvider, err = remotesandbox.NewProvider(remotesandbox.Options{
				BaseURL: cfg.Sandbox.RemoteURL, Headers: cfg.Sandbox.RemoteHeaders,
				VirtualRoot: sandboxVirtualRoot, ExecTimeout: cfg.Sandbox.ExecTimeout,
			})
			if err != nil {
				return builtAgent{}, err
			}
		}
		if err := registry.RegisterAll(builtin.All()...); err != nil {
			return builtAgent{}, err
		}
	}
	if err := registry.Register(mw.ClarificationTool()); err != nil {
		return builtAgent{}, err
	}
	if err := registry.Register(builtin.WriteTodos()); err != nil {
		return builtAgent{}, err
	}
	for _, toolConfig := range cfg.Tools {
		if toolConfig.Name != "web_search" && toolConfig.Name != "web_fetch" && toolConfig.Name != "image_search" {
			continue
		}
		apiKey := toolConfig.APIKey
		if apiKey == "" {
			if toolConfig.Name == "web_fetch" {
				apiKey = os.Getenv("JINA_API_KEY")
			} else {
				apiKey = os.Getenv("TAVILY_API_KEY")
			}
		}
		definition, definitionErr := community.Definition(community.Options{Name: toolConfig.Name, APIKey: apiKey, MaxResults: toolConfig.MaxResults, Timeout: toolConfig.Timeout})
		if definitionErr != nil {
			return builtAgent{}, definitionErr
		}
		if err := registry.Register(definition); err != nil {
			return builtAgent{}, err
		}
	}
	for name, server := range cfg.Extensions.MCPServers {
		if !server.Enabled {
			continue
		}
		var transport mcp.Transport
		switch server.Type {
		case "http", "sse":
			transport = &mcp.HTTPTransport{URL: server.URL, Headers: server.Headers, Client: &http.Client{Timeout: 30 * time.Second}}
		case "stdio":
			stdio, startErr := mcp.NewStdioTransportWithEnv(context.Background(), server.Command, server.Env, server.Args...)
			if startErr != nil {
				slog.Warn("MCP server unavailable; tools skipped", "server", name, "error", startErr)
				continue
			}
			transport = stdio
		}
		prefix := server.Prefix
		if prefix == "" {
			prefix = name
		}
		client := mcp.New(transport, prefix)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := client.RegisterAvailable(ctx, registry, slog.Default())
		cancel()
		if err != nil {
			_ = client.Close()
			return builtAgent{}, fmt.Errorf("agentd: registering MCP server %q: %w", name, err)
		}
		closers = append(closers, client.Close)
	}
	if cfg.Plugins.Enabled {
		reserved := append(registry.Names(), "task", "team_create", "team_delete", "list_teammates", "send_message", "invoke_acp_agent")
		contributions, pluginErr := plugin.Load(cfg.Plugins.Directories, reserved)
		if pluginErr != nil {
			return builtAgent{}, pluginErr
		}
		for _, contribution := range contributions {
			if _, modeErr := permission.ParseMode(contribution.RequiredPermission); modeErr != nil {
				return builtAgent{}, fmt.Errorf("agentd: plugin tool %q: %w", contribution.Definition.Name, modeErr)
			}
			if err := registry.Register(contribution.Definition); err != nil {
				return builtAgent{}, err
			}
		}
	}
	if len(cfg.ACPAgents) > 0 {
		agents := make(map[string]acp.AgentConfig, len(cfg.ACPAgents))
		for name, agentConfig := range cfg.ACPAgents {
			agents[name] = acp.AgentConfig{
				Command: agentConfig.Command, Args: agentConfig.Args, Env: agentConfig.Env,
				Description: agentConfig.Description, Model: agentConfig.Model,
				AutoApprovePermissions: agentConfig.AutoApprovePermissions,
			}
		}
		definition, acpErr := acp.Tool(acp.Options{Agents: agents, WorkRoot: filepath.Join(cfg.Sandbox.BaseDir, "acp-workspace")})
		if acpErr != nil {
			return builtAgent{}, acpErr
		}
		if err := registry.Register(definition); err != nil {
			return builtAgent{}, err
		}
	}
	policy, err := permission.NewPolicy(permission.Config{Mode: cfg.Permissions.Mode, ToolOverrides: cfg.Permissions.ToolOverrides})
	if err != nil {
		return builtAgent{}, err
	}
	var swarmManager *swarm.Manager
	if cfg.Swarm.Enabled && pool == nil {
		return builtAgent{}, errors.New("agentd: swarm requires runtime.database_url")
	}
	if pool != nil {
		swarmManager, err = swarm.New(pool, swarm.Options{MaxTeamSize: cfg.Swarm.MaxTeamSize})
		if err != nil {
			return builtAgent{}, err
		}
		if err := registry.RegisterAll(swarmManager.Tools()...); err != nil {
			return builtAgent{}, err
		}
	}
	var skillRegistry *skill.Registry
	var promptSkills []prompt.Skill
	if cfg.Skills.Enabled {
		if cfg.Skills.Path == "" {
			return builtAgent{}, errors.New("agentd: skills.path is required when skills are enabled")
		}
		skillRegistry, err = skill.LoadFiltered(cfg.Skills.Path, cfg.Skills.Disabled)
		if err != nil {
			return builtAgent{}, fmt.Errorf("agentd: loading skills: %w", err)
		}
		for _, meta := range skillRegistry.List() {
			loaded, getErr := skillRegistry.Get(meta.Name)
			if getErr != nil {
				return builtAgent{}, getErr
			}
			promptSkills = append(promptSkills, prompt.Skill{Name: meta.Name, Description: meta.Description, Path: loaded.Path})
		}
	}
	telemetryMiddleware := telemetry.New("nous-agent-go")
	var hookMiddleware middleware.Middleware
	var hookRunner *hooks.Runner
	var configuredHooks []hooks.Hook
	for _, hookConfig := range cfg.Hooks {
		if !hookConfig.Enabled {
			continue
		}
		var matcher *regexp.Regexp
		if hookConfig.Matcher != "" {
			matcher = regexp.MustCompile(hookConfig.Matcher) // validated by config.Load
		}
		configuredHooks = append(configuredHooks, &hooks.CommandHook{
			HookName: hookConfig.Name,
			OnEvents: hookConfig.Events,
			Command:  hookConfig.Command,
			Args:     hookConfig.Args,
			Matcher:  matcher,
			Timeout:  hookConfig.Timeout,
		})
	}
	if len(configuredHooks) > 0 {
		var runnerErr error
		hookRunner, runnerErr = hooks.NewRunner(configuredHooks, func(name string, err error) {
			slog.Warn("hook failed open", "hook", name, "error", err)
		})
		if runnerErr != nil {
			return builtAgent{}, runnerErr
		}
		hookMiddleware = mw.NewHook(hookRunner)
	}
	var guardrailInput middleware.Middleware
	var guardrailOutput middleware.Middleware
	if cfg.Guardrails.Enabled {
		provider, providerErr := guardrail.NewModelProvider(runtime.InstrumentModel(m, runtime.BucketMiddleware, "guardrail"))
		if providerErr != nil {
			return builtAgent{}, providerErr
		}
		evaluator, evaluatorErr := guardrail.New(provider, cfg.Guardrails.FailClosed)
		if evaluatorErr != nil {
			return builtAgent{}, evaluatorErr
		}
		if cfg.Guardrails.Input {
			guardrailInput = mw.NewGuardrailInput(evaluator)
		}
		if cfg.Guardrails.Output {
			guardrailOutput = mw.NewGuardrailOutput(evaluator)
		}
	}
	toolSet := runtimeToolSet{
		registry:        registry,
		policy:          policy,
		defaultSubagent: cfg.Subagents.Enabled,
		defaultSwarm:    cfg.Swarm.Enabled,
	}
	baseChain := func() []middleware.Middleware {
		chain := []middleware.Middleware{
			mw.NewThreadData(cfg.Sandbox.BaseDir, true),
			mw.NewUploads(),
			mw.NewTodo(),
			router.RuntimeOptions(),
			mw.NewViewImage(),
			mw.NewSwarmSession(swarmManager),
			telemetryMiddleware,
			mw.NewDanglingToolCall(),
			mw.NewPermission(policy, registry),
			mw.NewSandboxAudit(mw.AuditOptions{}),
			mw.NewToolErrorHandling(),
			mw.NewToolOutputBudget(mw.BudgetOptions{ExemptTools: []string{"read_file"}}),
			mw.NewRuntimeEvents(),
			mw.NewTokenUsage(),
		}
		if hookMiddleware != nil {
			chain = append(chain, hookMiddleware)
		}
		if guardrailInput != nil {
			chain = append(chain, guardrailInput)
		}
		if guardrailOutput != nil {
			chain = append(chain, guardrailOutput)
		}
		chain = append(chain,
			mw.NewSubagentLimit(cfg.Subagents.MaxConcurrent),
			mw.NewLoopDetection(mw.LoopDetectionOptions{}),
			mw.NewSafetyFinishReason(),
		)
		if swarmManager != nil {
			chain = append(chain, mw.NewInboxPoller(swarmManager, cfg.Swarm.PollLimit, cfg.Swarm.MessagePollInterval))
		}
		return chain
	}
	subagentDefs := subagent.Builtins()
	promptSubagents := make([]prompt.Subagent, 0, len(subagentDefs))
	for _, def := range subagentDefs {
		promptSubagents = append(promptSubagents, prompt.Subagent{Name: def.Name, Description: def.Description})
	}
	promptACPAgents := make([]prompt.Subagent, 0, len(cfg.ACPAgents))
	for name, agentConfig := range cfg.ACPAgents {
		promptACPAgents = append(promptACPAgents, prompt.Subagent{Name: name, Description: agentConfig.Description})
	}
	promptOptions := prompt.ProductionOptions{
		WorkspaceRoot: sandboxVirtualRoot,
		Skills:        promptSkills,
		Subagents:     promptSubagents,
		ACPAgents:     promptACPAgents,
		MaxConcurrent: cfg.Subagents.MaxConcurrent,
	}
	childPrompt := prompt.Production(promptOptions, prompt.RuntimeOptions{}) + delegatedTaskPrompt
	manager := subagent.NewManager(func(def subagent.Definition, allowed []string) (*loop.Runner, error) {
		childChain := baseChain()
		childDeclared := baseMiddlewareNames(cfg, hookMiddleware != nil, swarmManager != nil)
		if cfg.Skills.Enabled {
			childChain = append(childChain, mw.NewSkillActivation(skillRegistry, registry, cfg.Skills.Force))
			childDeclared = append(childDeclared, mw.NameSkillActivation)
		}
		childChain = append(childChain, mw.NewClarification())
		childDeclared = append(childDeclared, middleware.TerminalName)
		child, err := harness.New(harness.Options{Sampler: router, Registry: registry, Middleware: childChain, DeclaredMiddleware: childDeclared, ToolSet: newRestrictedToolSet(toolSet, allowed), Publisher: subagentProgressPublisher{}, Compactor: compactor, Limits: loop.Limits{MaxIterations: def.MaxTurns, Deadline: cfg.Loop.Deadline, MaxTokens: cfg.Loop.TokenBudget, MaxCostMicros: cfg.Loop.CostBudgetMicros, StopReinjectionLimit: cfg.Loop.StopReinjectionLimit}, MiddlewareTimeout: cfg.Loop.MiddlewareTimeout, ToolConcurrency: cfg.Loop.ToolConcurrency, TokenLimit: cfg.Loop.TokenBudget})
		if err != nil {
			return nil, err
		}
		return child.Runner(), nil
	}, taskStore, cfg.Subagents.TaskTTL, cfg.Subagents.MaxConcurrent)
	manager.SetHookRunner(hookRunner)
	if swarmManager != nil {
		manager.SetLifecycle(newSwarmSubagentLifecycle(swarmManager))
	}
	manager.SetTimeoutResolver(func(parent runtime.RunContext, req subagent.DispatchRequest) time.Duration {
		return subagentTimeout(cfg, parent, req)
	})
	for _, def := range subagentDefs {
		if def.MaxTurns > cfg.Subagents.MaxTurns {
			def.MaxTurns = cfg.Subagents.MaxTurns
		}
		def.SystemPrompt = childPrompt + fmt.Sprintf("\n\n<subagent_profile>\nName: %s\nPurpose: %s\n\n%s\n</subagent_profile>", def.Name, def.Description, def.SystemPrompt)
		if err := manager.Register(def); err != nil {
			return builtAgent{}, err
		}
	}
	if err := registry.Register(telemetryMiddleware.InstrumentToolDefinition(manager.TaskTool())); err != nil {
		return builtAgent{}, err
	}
	chain := baseChain()
	if cfg.Memory.Enabled {
		var store mem.Store = mem.NewMemoryStore()
		if pool != nil {
			store, err = mem.NewPostgresStore(pool)
			if err != nil {
				return builtAgent{}, err
			}
		}
		manager, managerErr := mem.New(runtime.InstrumentModel(m, runtime.BucketMiddleware, "memory"), store, mem.Options{MaxFacts: cfg.Memory.MaxFacts, ConfidenceThreshold: cfg.Memory.ConfidenceThreshold, InjectionTokens: cfg.Memory.InjectionTokens})
		if managerErr != nil {
			return builtAgent{}, managerErr
		}
		chain = append(chain, mw.NewMemory(manager))
	}
	if cfg.Title.Enabled {
		chain = append(chain, mw.NewTitle(runtime.InstrumentModel(m, runtime.BucketMiddleware, "title"), cfg.Title.MaxWords, cfg.Title.MaxChars))
	}
	if cfg.Skills.Enabled {
		chain = append(chain, mw.NewSkillActivation(skillRegistry, registry, cfg.Skills.Force))
	}
	chain = append(chain, mw.NewClarification())
	declared := append(baseMiddlewareNames(cfg, hookMiddleware != nil, swarmManager != nil), middleware.TerminalName)
	if cfg.Memory.Enabled {
		declared = append(declared, mw.NameMemory)
	}
	if cfg.Title.Enabled {
		declared = append(declared, mw.NameTitle)
	}
	if cfg.Skills.Enabled {
		declared = append(declared, mw.NameSkillActivation)
	}
	h, err := harness.New(harness.Options{Sampler: router, Registry: registry, Middleware: chain, DeclaredMiddleware: declared, ToolSet: toolSet, Compactor: compactor, Limits: loop.Limits{MaxIterations: cfg.Loop.MaxIterations, Deadline: cfg.Loop.Deadline, MaxTokens: cfg.Loop.TokenBudget, MaxCostMicros: cfg.Loop.CostBudgetMicros, StopReinjectionLimit: cfg.Loop.StopReinjectionLimit}, MiddlewareTimeout: cfg.Loop.MiddlewareTimeout, ToolConcurrency: cfg.Loop.ToolConcurrency, TokenLimit: cfg.Loop.TokenBudget})
	if err != nil {
		return builtAgent{}, err
	}
	promptBuilder := func(values map[string]any) string {
		swarmEnabled := mapBool(values, valueSwarmEnabled, cfg.Swarm.Enabled)
		subagentEnabled := mapBool(values, valueSubagentEnabled, cfg.Subagents.Enabled) || swarmEnabled
		return prompt.Production(promptOptions, prompt.RuntimeOptions{Subagents: subagentEnabled, Swarm: swarmEnabled})
	}
	initialValues := map[string]any{
		valueSubagentEnabled:       cfg.Subagents.Enabled,
		valueSwarmEnabled:          cfg.Swarm.Enabled,
		modelrouter.ValueModelName: mc.Name,
	}
	return builtAgent{agent: langgraphapi.HarnessAgent{Runner: h.Runner(), SystemPrompt: promptBuilder(initialValues), SystemPromptBuilder: promptBuilder, Sandbox: sandboxProvider, InitialValues: initialValues}, tools: registry.Names(), pricer: buildPricer(cfg.Models), close: closers, chain: h.Chain()}, nil
}

func subagentTimeout(cfg config.Config, parent runtime.RunContext, req subagent.DispatchRequest) time.Duration {
	if valueBool(parent.Values, valueSwarmEnabled) {
		return cfg.Swarm.TeammateTimeout
	}
	name := strings.TrimSpace(req.SubagentType)
	if name == "" {
		name = strings.TrimSpace(req.Agent)
	}
	return cfg.Subagents.TimeoutFor(name)
}

func buildModel(mc config.ModelConfig) (model.Model, error) {
	providerConfig := model.ProviderConfig{Name: mc.Name, Model: mc.Model, BaseURL: mc.BaseURL, APIKey: mc.APIKey, MaxTokens: mc.MaxTokens, Temperature: mc.Temperature, ContextLength: mc.ContextLength, SupportsThinking: mc.SupportsThinking, SupportsReasoningEffort: mc.SupportsReasoningEffort, SupportsVision: mc.SupportsVision, ExtraBody: mc.ExtraBody, Timeout: mc.Timeout}
	if mc.WhenThinkingEnabled != nil {
		providerConfig.ThinkingExtraBody = mc.WhenThinkingEnabled.ExtraBody
	}
	switch mc.Provider {
	case openai.Name:
		return openai.New(providerConfig)
	case anthropic.Name:
		return anthropic.New(providerConfig)
	default:
		return nil, fmt.Errorf("agentd: unsupported model provider %q for model %q", mc.Provider, mc.Name)
	}
}

func buildPricer(models []config.ModelConfig) *runtime.Pricer {
	prices := make(map[string]runtime.Price, len(models)*2)
	for _, configured := range models {
		price := runtime.Price{
			InputPerMillion:       configured.Pricing.InputPerMillionMicros,
			OutputPerMillion:      configured.Pricing.OutputPerMillionMicros,
			CachedInputPerMillion: configured.Pricing.CachedInputPerMillionMicros,
		}
		// Providers report the upstream model ID while runtime selection uses the
		// configured alias. Register both so direct and fallback calls agree.
		prices[configured.Name] = price
		prices[configured.Model] = price
	}
	return runtime.NewPricer(prices, runtime.Price{})
}

func baseMiddlewareNames(cfg config.Config, hasHooks, hasSwarm bool) []string {
	names := []string{
		mw.NameThreadData, mw.NameUploads, mw.NameTodo, modelrouter.NameRuntimeOptions,
		mw.NameViewImage, mw.NameSwarmSession,
		telemetry.Name, mw.NameDanglingToolCall, mw.NamePermission,
		mw.NameSandboxAudit, mw.NameToolErrorHandling, mw.NameToolOutputBudget,
		mw.NameRuntimeEvents, mw.NameTokenUsage,
	}
	if hasHooks {
		names = append(names, mw.NameHook)
	}
	if cfg.Guardrails.Enabled && cfg.Guardrails.Input {
		names = append(names, mw.NameGuardrailInput)
	}
	if cfg.Guardrails.Enabled && cfg.Guardrails.Output {
		names = append(names, mw.NameGuardrailOutput)
	}
	names = append(names, mw.NameSubagentLimit, mw.NameLoopDetection, mw.NameSafetyFinishReason)
	if hasSwarm {
		names = append(names, mw.NameInboxPoller)
	}
	return names
}

func mapBool(values map[string]any, key string, fallback bool) bool {
	value, ok := values[key]
	if !ok || value == nil {
		return fallback
	}
	enabled, ok := value.(bool)
	return ok && enabled
}

func publishModelStreamEvent(ctx context.Context, st *middleware.State, ev model.StreamEvent) {
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.Publish == nil {
		return
	}
	messageID := fmt.Sprintf("%s:%d", st.RunID, st.Iteration)
	if run.SubagentTaskID != "" {
		// Child replies are projected as complete task_running messages after the
		// model phase. Streaming their raw deltas here would mix them into the
		// lead conversation and expose partial tool-call JSON.
		return
	}
	eventRunID := run.EventStreamRunID()
	var event runtime.Event
	switch ev.Type {
	case model.StreamStart:
		event = runtime.MustEvent(eventRunID, run.ThreadID, runtime.EventMessageStart, map[string]any{"message_id": messageID})
	case model.StreamTextDelta:
		if ev.Delta == "" {
			return
		}
		event = runtime.MustEvent(eventRunID, run.ThreadID, runtime.EventContentDelta, runtime.ContentDelta{Delta: ev.Delta, MessageID: messageID})
	case model.StreamThinkingDelta:
		if ev.Delta == "" {
			return
		}
		event = runtime.MustEvent(eventRunID, run.ThreadID, runtime.EventReasoningDelta, runtime.ReasoningDelta{Delta: ev.Delta, MessageID: messageID})
	case model.StreamDone:
		event = runtime.MustEvent(eventRunID, run.ThreadID, runtime.EventMessageStop, map[string]any{"message_id": messageID})
	default:
		return
	}
	run.Publish(ctx, event)
}

type subagentProgressPublisher struct{}

func (subagentProgressPublisher) PublishReply(ctx context.Context, st *middleware.State, _ bool) {
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.Publish == nil || run.SubagentTaskID == "" || st == nil || st.ModelOutput == nil {
		return
	}
	run.Publish(ctx, runtime.MustEvent(run.EventStreamRunID(), run.ThreadID, runtime.EventSubagentProgress, runtime.SubagentProgress{
		TaskID:       run.SubagentTaskID,
		MessageID:    fmt.Sprintf("%s:%d", st.RunID, st.Iteration),
		MessageIndex: st.Iteration + 1,
		Message:      st.ModelOutput.Message,
	}))
}
