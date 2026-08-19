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

	"github.com/KyrieWang7/nous-agent/agent-go/internal/transport/httpapi"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/acp"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/compaction"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/config"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/guardrail"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/harness"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/hooks"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/mcp"
	mem "github.com/KyrieWang7/nous-agent/agent-go/pkg/memory"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/anthropic"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/openai"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/modelrouter"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/permission"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/plugin"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/prompt"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	lifecyclehandlers "github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle/handlers"
	runtimeplugin "github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/plugin"
	runtimepostgres "github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/postgres"
	runtimeredis "github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/redis"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/runmanager"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox/aio"
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
messaging tools are available, use send_message with to="lead" to report
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

	apiOpts := httpapi.Options{HeartbeatInterval: cfg.Runtime.HeartbeatInterval, Pricer: buildPricer(cfg.Models)}
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
		apiOpts.Store, err = httpapi.NewPostgresStore(pool)
		if err != nil {
			return err
		}
		postgresEvents, err = runtimepostgres.NewEventStore(pool)
		if err != nil {
			return err
		}
		apiOpts.EventStore = postgresEvents
		apiOpts.MetadataStore, err = runtimepostgres.NewMetadataStore(pool)
		if err != nil {
			return err
		}
		apiOpts.Snapshots, err = runtimepostgres.NewSnapshotStore(pool)
		if err != nil {
			return err
		}
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
		taskStore, err = runtimeredis.NewTaskStore(client)
		if err != nil {
			return err
		}
		redisEvents := runtimeredis.NewEventStore(client, cfg.Runtime.EventTTL)
		apiOpts.EventStore = redisEvents
		if postgresEvents != nil {
			apiOpts.EventStore = runtime.MultiEventStore{Primary: redisEvents, Mirrors: []runtime.EventStore{postgresEvents}}
		}
		apiOpts.Registry, err = runtime.NewRegistry(context.Background(), runtime.RegistryOptions{Backend: runtimeredis.NewRegistryBackend(client), Owner: cfg.Server.Address, TTL: cfg.Runtime.EventTTL})
		if err != nil {
			return err
		}
	}
	if apiOpts.Registry != nil {
		apiOpts.OwnRegistry = true
	}
	var approvalStore runtime.ApprovalStore = runtime.NewMemoryApprovalStore()
	if pool != nil {
		approvalStore, err = runtimepostgres.NewApprovalStore(pool)
		if err != nil {
			return err
		}
	}
	approvalManager, err := runtime.NewApprovalManager(approvalStore)
	if err != nil {
		return err
	}
	apiOpts.Approvals = approvalManager
	var questionStore runtime.QuestionStore = runtime.NewMemoryQuestionStore()
	if pool != nil {
		questionStore, err = runtimepostgres.NewQuestionStore(pool)
		if err != nil {
			return err
		}
	}
	questionManager, err := runtime.NewQuestionManager(questionStore)
	if err != nil {
		return err
	}
	apiOpts.Questions = questionManager
	built, err := buildAgent(cfg, taskStore, pool, approvalManager)
	if err != nil {
		return err
	}
	reloadable, err := newReloadableAgent(*configPath, cfg, built, func(reloaded config.Config) (builtAgent, error) {
		return buildAgent(reloaded, taskStore, pool, approvalManager)
	})
	if err != nil {
		built.Close()
		return err
	}
	defer reloadable.Close()
	apiOpts.Agent = reloadable
	apiOpts.AllowedTools = built.tools
	api, err := httpapi.New(apiOpts)
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
	slog.Info("Nous agent-go listening", "address", cfg.Server.Address, "api", "/api/v1")
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
	agent        runmanager.Agent
	tools        []string
	pricer       *runtime.Pricer
	budget       runtime.BudgetAmount
	maxDepth     int
	capabilities []string
	close        []func() error
	chain        *lifecycle.Dispatcher
}

func (b builtAgent) Close() {
	for i := len(b.close) - 1; i >= 0; i-- {
		if err := b.close[i](); err != nil {
			slog.Warn("closing agent dependency", "error", err)
		}
	}
}

func buildAgent(cfg config.Config, taskStore subagent.TaskStore, pool *pgxpool.Pool, approvalManagers ...*runtime.ApprovalManager) (builtAgent, error) {
	capabilities := capability.NewRegistry()
	generationID := fmt.Sprintf("agentd-%d", time.Now().UTC().UnixNano())
	var closers []func() error
	assembled := false
	defer func() {
		if assembled {
			return
		}
		for i := len(closers) - 1; i >= 0; i-- {
			_ = closers[i]()
		}
	}()
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
		if closer, ok := configured.(interface{ Close() error }); ok {
			closers = append(closers, closer.Close)
		}
		namedModels[modelConfig.Name] = configured
		if err := registerRuntimeValue(capabilities, capability.Definition{
			Name: "model." + modelConfig.Name, Kind: capability.KindModel,
			Description: modelConfig.Model, Scope: capability.ScopeGlobal,
		}, configured); err != nil {
			return builtAgent{}, err
		}
	}
	m := namedModels[mc.Name]
	if m == nil {
		return builtAgent{}, fmt.Errorf("agentd: default model %q was not built", mc.Name)
	}
	if err := registerRuntimeValue(capabilities, capability.Definition{
		Name: "model.default", Kind: capability.KindModel,
		Description: mc.Model, Scope: capability.ScopeGlobal,
	}, m); err != nil {
		return builtAgent{}, err
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
		}, compaction.NewModelSummariser(runtime.InstrumentModel(m, runtime.BucketAuxiliary, "compaction")))
		if err != nil {
			return builtAgent{}, err
		}
	}
	router, err := modelrouter.New(modelrouter.Config{
		Models:            map[modelrouter.Tier]model.Model{modelrouter.TierStandard: m},
		NamedModels:       namedModels,
		TierCapabilities:  map[modelrouter.Tier]string{modelrouter.TierStandard: "model.default"},
		NamedCapabilities: modelCapabilityNames(namedModels),
		Stream:            true,
		Compactor:         compactor,
		OnStreamEvent:     publishModelStreamEvent,
	})
	if err != nil {
		return builtAgent{}, err
	}

	registry := tool.NewRegistry()
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
				TenantID: cfg.Sandbox.TenantID, VirtualRoot: sandboxVirtualRoot, ExecTimeout: cfg.Sandbox.ExecTimeout,
			})
			if err != nil {
				return builtAgent{}, err
			}
		case "aio":
			sandboxProvider, err = aio.NewProvider(aio.Options{ProvisionerURL: cfg.Sandbox.ProvisionerURL, Headers: cfg.Sandbox.RemoteHeaders, VirtualRoot: sandboxVirtualRoot, ExecTimeout: cfg.Sandbox.ExecTimeout})
			if err != nil {
				return builtAgent{}, err
			}
		}
		if err := registry.RegisterAll(builtin.All()...); err != nil {
			return builtAgent{}, err
		}
		if err := registerRuntimeValue(capabilities, capability.Definition{
			Name: "sandbox.default", Kind: capability.KindSandbox,
			Description: cfg.Sandbox.Provider, Scope: capability.ScopeThread,
		}, sandboxProvider); err != nil {
			return builtAgent{}, err
		}
	}
	if err := registry.Register(lifecyclehandlers.ClarificationTool()); err != nil {
		return builtAgent{}, err
	}
	if err := registry.Register(builtin.WriteTodos()); err != nil {
		return builtAgent{}, err
	}
	if err := registry.Register(builtin.ExitPlanMode("interaction.questions")); err != nil {
		return builtAgent{}, err
	}
	if err := capabilities.Register(capability.Entry{
		Definition: capability.Definition{Name: "interaction.questions", Kind: capability.KindInteraction, Description: "durable user question channel", Scope: capability.ScopeRun},
		Resolver: func(ctx context.Context, _ capability.ResolveRequest) (any, error) {
			run, ok := runtime.RunContextFrom(ctx)
			if !ok || run.Questions == nil {
				return nil, errors.New("agentd: user question capability is unavailable")
			}
			return run.Questions, nil
		},
	}); err != nil {
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
		if err := registerRuntimeValue(capabilities, capability.Definition{
			Name: "mcp." + name, Kind: capability.KindMCP,
			Description: server.Type, Scope: capability.ScopeGlobal,
		}, client); err != nil {
			_ = client.Close()
			return builtAgent{}, err
		}
		closers = append(closers, client.Close)
	}
	var runtimePlugins []runtimeplugin.Plugin
	if cfg.Plugins.Enabled {
		reserved := append(registry.Names(), "task", "swarm_batch", "team_create", "team_delete", "list_teammates", "send_message", "invoke_acp_agent")
		contributions, pluginErr := plugin.Load(cfg.Plugins.Directories, reserved)
		if pluginErr != nil {
			return builtAgent{}, pluginErr
		}
		for _, contribution := range contributions {
			if _, modeErr := permission.ParseSandboxMode(contribution.RequiredSandboxMode); modeErr != nil {
				return builtAgent{}, fmt.Errorf("agentd: plugin tool %q: %w", contribution.Definition.Name, modeErr)
			}
		}
		runtimePlugins = commandToolPlugins(contributions)
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
	var approvalManager *runtime.ApprovalManager
	if len(approvalManagers) > 0 {
		approvalManager = approvalManagers[0]
	}
	approvalTTL := cfg.Permissions.ApprovalTTL
	if approvalTTL <= 0 {
		approvalTTL = cfg.Permissions.PromptTimeout
	}
	if approvalTTL <= 0 {
		approvalTTL = 2 * time.Minute
	}
	policy, err := permission.NewPolicy(permission.Config{
		Preset: cfg.Permissions.Preset, Presets: cfg.Permissions.Presets,
		Prompter:      &permission.DurablePrompter{Manager: approvalManager, TTL: approvalTTL},
		PromptTimeout: cfg.Permissions.PromptTimeout,
	})
	if err != nil {
		return builtAgent{}, err
	}
	if err := registerRuntimeValue(capabilities, capability.Definition{
		Name: "policy.tools", Kind: capability.KindPolicy,
		Description: string(cfg.Permissions.Preset), Scope: capability.ScopeRun,
	}, policy); err != nil {
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
			promptSkills = append(promptSkills, prompt.Skill{Name: meta.Name, Description: meta.Description})
			if err := registerRuntimeValue(capabilities, capability.Definition{
				Name: "skill." + meta.Name, Kind: capability.KindSkill,
				Description: meta.Description, Scope: capability.ScopeGlobal,
			}, loaded); err != nil {
				return builtAgent{}, err
			}
		}
		if err := registerRuntimeValue(capabilities, capability.Definition{
			Name: "skill.registry", Kind: capability.KindSkill,
			Description: "loaded skill catalog", Scope: capability.ScopeGlobal,
		}, skillRegistry); err != nil {
			return builtAgent{}, err
		}
	}
	telemetryObserver := telemetry.New("nous-agent-go")
	var hookHandler lifecycle.Handler
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
		hookHandler = lifecyclehandlers.NewHook(hookRunner)
	}
	var guardrailInput lifecycle.Handler
	var guardrailOutput lifecycle.Handler
	if cfg.Guardrails.Enabled {
		provider, providerErr := guardrail.NewModelProvider(runtime.InstrumentModel(m, runtime.BucketAuxiliary, "guardrail"))
		if providerErr != nil {
			return builtAgent{}, providerErr
		}
		evaluator, evaluatorErr := guardrail.New(provider, cfg.Guardrails.FailClosed)
		if evaluatorErr != nil {
			return builtAgent{}, evaluatorErr
		}
		if cfg.Guardrails.Input {
			guardrailInput = lifecyclehandlers.NewGuardrailInput(evaluator)
		}
		if cfg.Guardrails.Output {
			guardrailOutput = lifecyclehandlers.NewGuardrailOutput(evaluator)
		}
	}
	toolSet := runtimeToolSet{
		registry:         registry,
		policyCapability: "policy.tools",
		defaultSubagent:  cfg.Subagents.Enabled,
		defaultSwarm:     cfg.Swarm.Enabled,
	}
	planPolicy, err := lifecyclehandlers.NewPlanPolicy(cfg.Plan.Guidance)
	if err != nil {
		return builtAgent{}, err
	}
	baseLifecycle := func() []lifecycle.Handler {
		chain := []lifecycle.Handler{
			lifecyclehandlers.NewThreadData(cfg.Sandbox.BaseDir, true),
			lifecyclehandlers.NewUploads(),
			planPolicy,
			router.RuntimeOptions(),
			lifecyclehandlers.NewViewImage(),
			lifecyclehandlers.NewSwarmSession(swarmManager),
			telemetryObserver,
			lifecyclehandlers.NewDanglingToolCall(),
			lifecyclehandlers.NewPermission("policy.tools", registry),
			lifecyclehandlers.NewSandboxAudit(lifecyclehandlers.AuditOptions{}),
			lifecyclehandlers.NewToolErrorHandling(),
			lifecyclehandlers.NewToolOutputBudget(lifecyclehandlers.BudgetOptions{ExemptTools: []string{"read_file"}}),
		}
		if hookHandler != nil {
			chain = append(chain, hookHandler)
		}
		if guardrailInput != nil {
			chain = append(chain, guardrailInput)
		}
		if guardrailOutput != nil {
			chain = append(chain, guardrailOutput)
		}
		chain = append(chain,
			lifecyclehandlers.NewSubagentLimit(cfg.Subagents.MaxConcurrent),
			lifecyclehandlers.NewLoopDetection(lifecyclehandlers.LoopDetectionOptions{}),
			lifecyclehandlers.NewSafetyFinishReason(),
		)
		if swarmManager != nil {
			chain = append(chain, lifecyclehandlers.NewInboxPoller(swarmManager, cfg.Swarm.PollLimit, cfg.Swarm.MessagePollInterval))
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
		childLifecycle := baseLifecycle()
		childDeclared := baseLifecycleNames(cfg, hookHandler != nil, swarmManager != nil)
		if cfg.Skills.Enabled {
			childLifecycle = append(childLifecycle, lifecyclehandlers.NewSkillActivation("skill.registry", registry, cfg.Skills.Force))
			childDeclared = append(childDeclared, lifecyclehandlers.NameSkillActivation)
		}
		childLifecycle = append(childLifecycle, lifecyclehandlers.NewClarification())
		childDeclared = append(childDeclared, lifecycle.TerminalName)
		child, err := harness.New(harness.Options{Sampler: router, Registry: registry, LifecycleHandlers: childLifecycle, DeclaredHandlers: childDeclared, ToolSet: newRestrictedToolSet(toolSet, allowed), Publisher: subagentProgressPublisher{}, Compactor: compactor, Limits: loop.Limits{MaxIterations: def.MaxTurns, Deadline: cfg.Loop.Deadline, MaxTokens: cfg.Loop.TokenBudget, MaxCostMicros: cfg.Loop.CostBudgetMicros, MaxToolCalls: cfg.Loop.ToolCallBudget, MaxSubagents: cfg.Loop.SubagentBudget, StopReinjectionLimit: cfg.Loop.StopReinjectionLimit}, LifecycleTimeout: cfg.Loop.LifecycleTimeout, ToolConcurrency: cfg.Loop.ToolConcurrency, TokenLimit: cfg.Loop.TokenBudget, Capabilities: capabilities, GenerationID: generationID})
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
		definition := def
		if err := capabilities.Register(capability.Entry{
			Definition: capability.Definition{Name: "agent." + definition.Name, Kind: capability.KindAgent, Description: definition.Description, Scope: capability.ScopeRun},
			Resolver: func(context.Context, capability.ResolveRequest) (any, error) {
				return definition, nil
			},
		}); err != nil {
			return builtAgent{}, err
		}
	}
	if err := registerRuntimeValue(capabilities, capability.Definition{
		Name: "agent.subagents", Kind: capability.KindAgent,
		Description: "subagent dispatcher", Scope: capability.ScopeRun,
	}, manager); err != nil {
		return builtAgent{}, err
	}
	if err := registry.Register(telemetryObserver.InstrumentToolDefinition(manager.TaskTool())); err != nil {
		return builtAgent{}, err
	}
	if err := registry.Register(telemetryObserver.InstrumentToolDefinition(manager.SwarmBatchTool())); err != nil {
		return builtAgent{}, err
	}
	chain := baseLifecycle()
	if cfg.Memory.Enabled {
		var store mem.Store = mem.NewMemoryStore()
		if pool != nil {
			store, err = runtimepostgres.NewMemoryStore(pool)
			if err != nil {
				return builtAgent{}, err
			}
		}
		memoryManager, managerErr := mem.New(runtime.InstrumentModel(m, runtime.BucketAuxiliary, "memory"), store, mem.Options{MaxFacts: cfg.Memory.MaxFacts, ConfidenceThreshold: cfg.Memory.ConfidenceThreshold, InjectionTokens: cfg.Memory.InjectionTokens})
		if managerErr != nil {
			return builtAgent{}, managerErr
		}
		if err := registerRuntimeValue(capabilities, capability.Definition{
			Name: "memory.store", Kind: capability.KindMemory,
			Description: "durable scoped memory store", Scope: capability.ScopeThread,
		}, store); err != nil {
			return builtAgent{}, err
		}
		if err := registerRuntimeValue(capabilities, capability.Definition{
			Name: "memory.manager", Kind: capability.KindMemory,
			Description: "memory extraction and prompt projection", Scope: capability.ScopeThread,
		}, memoryManager); err != nil {
			return builtAgent{}, err
		}
		chain = append(chain, lifecyclehandlers.NewMemory("memory.manager"))
	}
	if cfg.Title.Enabled {
		chain = append(chain, lifecyclehandlers.NewTitle(runtime.InstrumentModel(m, runtime.BucketAuxiliary, "title"), cfg.Title.MaxWords, cfg.Title.MaxChars))
	}
	if cfg.Skills.Enabled {
		chain = append(chain, lifecyclehandlers.NewSkillActivation("skill.registry", registry, cfg.Skills.Force))
	}
	chain = append(chain, lifecyclehandlers.NewClarification())
	declared := append(baseLifecycleNames(cfg, hookHandler != nil, swarmManager != nil), lifecycle.TerminalName)
	if cfg.Memory.Enabled {
		declared = append(declared, lifecyclehandlers.NameMemory)
	}
	if cfg.Title.Enabled {
		declared = append(declared, lifecyclehandlers.NameTitle)
	}
	if cfg.Skills.Enabled {
		declared = append(declared, lifecyclehandlers.NameSkillActivation)
	}
	h, err := harness.New(harness.Options{Sampler: router, Registry: registry, LifecycleHandlers: chain, DeclaredHandlers: declared, ToolSet: toolSet, StopGate: planPolicy, Compactor: compactor, Limits: loop.Limits{MaxIterations: cfg.Loop.MaxIterations, Deadline: cfg.Loop.Deadline, MaxTokens: cfg.Loop.TokenBudget, MaxCostMicros: cfg.Loop.CostBudgetMicros, MaxToolCalls: cfg.Loop.ToolCallBudget, MaxSubagents: cfg.Loop.SubagentBudget, StopReinjectionLimit: cfg.Loop.StopReinjectionLimit}, LifecycleTimeout: cfg.Loop.LifecycleTimeout, ToolConcurrency: cfg.Loop.ToolConcurrency, TokenLimit: cfg.Loop.TokenBudget, Capabilities: capabilities, Plugins: runtimePlugins, GenerationID: generationID})
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
	closers = append(closers, h.Close)
	sandboxCapability := ""
	if sandboxProvider != nil {
		sandboxCapability = "sandbox.default"
	}
	assembled = true
	budgetLimits := runtime.BudgetAmount{Tokens: int64(cfg.Loop.TokenBudget), CostMicros: cfg.Loop.CostBudgetMicros, ToolCalls: int64(cfg.Loop.ToolCallBudget), Subagents: int64(cfg.Loop.SubagentBudget)}
	capabilityNames := h.Runtime().Generation.Capabilities().Names()
	return builtAgent{agent: harness.Agent{
		Runner: h.Runner(), SystemPrompt: promptBuilder(initialValues), SystemPromptBuilder: promptBuilder,
		InitialValues: initialValues, Capabilities: h.Runtime().Generation.Capabilities(),
		GenerationID: generationID, SandboxCapability: sandboxCapability, BudgetLimits: budgetLimits, MaxRecursionDepth: cfg.Loop.MaxRecursionDepth,
		AllowedCapabilities: capabilityNames,
	}, tools: registry.Names(), pricer: buildPricer(cfg.Models), budget: budgetLimits,
		capabilities: capabilityNames, maxDepth: cfg.Loop.MaxRecursionDepth, close: closers, chain: h.Lifecycle()}, nil
}

func registerRuntimeValue(registry *capability.Registry, definition capability.Definition, value any) error {
	if registry.Has(definition.Name) {
		return nil
	}
	if err := capability.RegisterValue(registry, capability.Value{Definition: definition, Value: value}); err != nil {
		return fmt.Errorf("agentd: registering runtime capability %q: %w", definition.Name, err)
	}
	return nil
}

func modelCapabilityNames(models map[string]model.Model) map[string]string {
	names := make(map[string]string, len(models))
	for name := range models {
		names[name] = "model." + name
	}
	return names
}

func subagentTimeout(cfg config.Config, parent runtime.RunContext, req subagent.DispatchRequest) time.Duration {
	if valueBool(parent.Values, valueSwarmEnabled) {
		return cfg.Swarm.TeammateTimeout
	}
	name := strings.TrimSpace(req.SubagentType)
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

func baseLifecycleNames(cfg config.Config, hasHooks, hasSwarm bool) []string {
	names := []string{
		lifecyclehandlers.NameThreadData, lifecyclehandlers.NameUploads, lifecyclehandlers.NamePlanPolicy, modelrouter.NameRuntimeOptions,
		lifecyclehandlers.NameViewImage, lifecyclehandlers.NameSwarmSession,
		telemetry.Name, lifecyclehandlers.NameDanglingToolCall, lifecyclehandlers.NamePermission,
		lifecyclehandlers.NameSandboxAudit, lifecyclehandlers.NameToolErrorHandling, lifecyclehandlers.NameToolOutputBudget,
	}
	if hasHooks {
		names = append(names, lifecyclehandlers.NameHook)
	}
	if cfg.Guardrails.Enabled && cfg.Guardrails.Input {
		names = append(names, lifecyclehandlers.NameGuardrailInput)
	}
	if cfg.Guardrails.Enabled && cfg.Guardrails.Output {
		names = append(names, lifecyclehandlers.NameGuardrailOutput)
	}
	names = append(names, lifecyclehandlers.NameSubagentLimit, lifecyclehandlers.NameLoopDetection, lifecyclehandlers.NameSafetyFinishReason)
	if hasSwarm {
		names = append(names, lifecyclehandlers.NameInboxPoller)
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

func publishModelStreamEvent(ctx context.Context, st *lifecycle.State, ev model.StreamEvent) {
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

func (subagentProgressPublisher) PublishReply(ctx context.Context, st *lifecycle.State, _ bool) {
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.Publish == nil || run.SubagentTaskID == "" || st == nil || st.ModelOutput == nil {
		return
	}
	_, _ = run.Publish(ctx, runtime.MustEvent(run.EventStreamRunID(), run.ThreadID, runtime.EventSubagentProgress, runtime.SubagentProgress{
		TaskID:       run.SubagentTaskID,
		MessageID:    fmt.Sprintf("%s:%d", st.RunID, st.Iteration),
		MessageIndex: st.Iteration + 1,
		Message:      st.ModelOutput.Message,
	}))
}
