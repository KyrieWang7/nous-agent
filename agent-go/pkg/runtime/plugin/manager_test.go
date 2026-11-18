package plugin_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/plugin"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

type testPlugin struct {
	name   string
	inject []string
	apply  func(*plugin.Context) error
}

func (p testPlugin) Name() string                  { return p.name }
func (p testPlugin) Inject() []string              { return p.inject }
func (p testPlugin) Apply(c *plugin.Context) error { return p.apply(c) }

func TestServicesActivateConsumersAndDisposeReverse(t *testing.T) {
	var log []string
	consumer := testPlugin{"consumer", []string{"database"}, func(c *plugin.Context) error {
		value, err := c.Service("database")
		if err != nil || value != "ready" {
			t.Fatalf("service=%v err=%v", value, err)
		}
		log = append(log, "apply:consumer")
		return c.OnDispose(func(context.Context) error { log = append(log, "dispose:consumer"); return nil })
	}}
	provider := testPlugin{"not-the-service-name", nil, func(c *plugin.Context) error {
		log = append(log, "apply:provider")
		if err := c.OnDispose(func(context.Context) error { log = append(log, "dispose:provider"); return nil }); err != nil {
			return err
		}
		return c.Provide("database", "ready")
	}}
	m, err := plugin.NewManager([]plugin.Plugin{consumer, provider})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Start(context.Background(), plugin.Host{}); err != nil {
		t.Fatal(err)
	}
	if err = m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"apply:provider", "apply:consumer", "dispose:consumer", "dispose:provider"}
	if !reflect.DeepEqual(log, want) {
		t.Fatalf("lifecycle=%v", log)
	}
}

func echo(name string) tool.Definition {
	return tool.Definition{Name: name, Group: "test", Handler: func(context.Context, tool.Call) (*tool.Result, error) { return &tool.Result{Content: "ok"}, nil }}
}

func TestFailingApplyRemovesPartialRegistrationsAndJoinsCleanupErrors(t *testing.T) {
	tools := tool.NewRegistry()
	caps := capability.NewRegistry()
	setupErr := errors.New("setup failed")
	cleanupErr := errors.New("cleanup failed")
	p := testPlugin{"broken", []string{plugin.ToolService, plugin.CapabilityService}, func(c *plugin.Context) error {
		if err := c.RegisterTool(echo("partial")); err != nil {
			return err
		}
		if err := c.RegisterCapability(capability.Entry{Definition: capability.Definition{Name: "memory.partial", Kind: capability.KindMemory}, Resolver: func(context.Context, capability.ResolveRequest) (any, error) { return "partial", nil }}); err != nil {
			return err
		}
		if err := c.OnDispose(func(context.Context) error { return cleanupErr }); err != nil {
			return err
		}
		return setupErr
	}}
	m, _ := plugin.NewManager([]plugin.Plugin{p})
	err := m.Start(context.Background(), plugin.Host{Tools: tools, Capabilities: caps})
	if !errors.Is(err, setupErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("error=%v", err)
	}
	if tools.Has("partial") || caps.Has("memory.partial") {
		t.Fatal("failed setup leaked registrations")
	}
	if !errors.Is(m.Stop(context.Background()), cleanupErr) {
		t.Fatal("cleanup result was lost")
	}
}

func TestMissingRequiredServiceRollsBackAndOptionalRemainsPending(t *testing.T) {
	var disposed bool
	provider := testPlugin{"provider", nil, func(c *plugin.Context) error {
		return c.OnDispose(func(context.Context) error { disposed = true; return nil })
	}}
	missing := testPlugin{"consumer", []string{"unavailable"}, func(*plugin.Context) error { t.Fatal("pending plugin was applied"); return nil }}
	m, _ := plugin.NewManager([]plugin.Plugin{provider, missing})
	if err := m.Start(context.Background(), plugin.Host{}); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("error=%v", err)
	}
	if !disposed {
		t.Fatal("required dependency failure leaked provider")
	}
	m, _ = plugin.NewManager([]plugin.Plugin{plugin.Optional{Plugin: missing}})
	if err := m.Start(context.Background(), plugin.Host{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.Pending()["consumer"], []string{"unavailable"}) {
		t.Fatal(m.Pending())
	}
	_ = m.Stop(context.Background())
}

func TestConcurrentStopWaitsForCanceledSetupAndOwnedWork(t *testing.T) {
	entered := make(chan struct{})
	workerStarted := make(chan struct{})
	workerCanceled := make(chan struct{})
	releaseWorker := make(chan struct{})
	p := testPlugin{"slow", nil, func(c *plugin.Context) error {
		if err := c.Go(func(ctx context.Context) { close(workerStarted); <-ctx.Done(); close(workerCanceled); <-releaseWorker }); err != nil {
			return err
		}
		close(entered)
		<-c.Done()
		return c.Err()
	}}
	m, _ := plugin.NewManager([]plugin.Plugin{p})
	startDone := make(chan error, 1)
	go func() { startDone <- m.Start(context.Background(), plugin.Host{}) }()
	<-entered
	<-workerStarted
	stops := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); stops <- m.Stop(context.Background()) }()
	}
	<-workerCanceled
	select {
	case err := <-stops:
		t.Fatalf("Stop returned before quiescence: %v", err)
	default:
	}
	close(releaseWorker)
	wg.Wait()
	if err := <-startDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("Start=%v", err)
	}
	for range 2 {
		if err := <-stops; err != nil {
			t.Fatal(err)
		}
	}
}

func TestDuplicateServiceFailsBeforeConsumerSeesItAndRemovesCatalog(t *testing.T) {
	tools := tool.NewRegistry()
	caps := capability.NewRegistry()
	p := testPlugin{"tools", []string{plugin.ToolService}, func(c *plugin.Context) error { return c.RegisterTool(echo("echo")) }}
	m, _ := plugin.NewManager([]plugin.Plugin{p})
	if err := m.Start(context.Background(), plugin.Host{Tools: tools, Capabilities: caps}); err != nil {
		t.Fatal(err)
	}
	if !caps.Has("tool.echo") {
		t.Fatal("plugin tool absent from generation catalog")
	}
	_ = m.Stop(context.Background())
	if tools.Has("echo") || caps.Has("tool.echo") {
		t.Fatal("tool contribution leaked after disposal")
	}
	bad := testPlugin{"duplicate", nil, func(c *plugin.Context) error { return c.Provide(plugin.ToolService, "bypass") }}
	m, _ = plugin.NewManager([]plugin.Plugin{bad})
	if err := m.Start(context.Background(), plugin.Host{Tools: tools}); err == nil {
		t.Fatal("duplicate service accepted")
	}
}

func TestApplyPanicRollsBackAndContextRejectsLateRegistration(t *testing.T) {
	tools := tool.NewRegistry()
	p := testPlugin{"panicking", []string{plugin.ToolService}, func(c *plugin.Context) error {
		if err := c.RegisterTool(echo("partial")); err != nil {
			return err
		}
		panic("broken setup")
	}}
	m, _ := plugin.NewManager([]plugin.Plugin{p})
	if err := m.Start(context.Background(), plugin.Host{Tools: tools}); err == nil {
		t.Fatal("setup panic disappeared")
	}
	if tools.Has("partial") {
		t.Fatal("panic leaked partial registrations")
	}
	var saved *plugin.Context
	p = testPlugin{"valid", []string{plugin.ToolService}, func(c *plugin.Context) error { saved = c; return nil }}
	m, _ = plugin.NewManager([]plugin.Plugin{p})
	if err := m.Start(context.Background(), plugin.Host{Tools: tools}); err != nil {
		t.Fatal(err)
	}
	if err := saved.RegisterTool(echo("too-late")); err == nil {
		t.Fatal("published generation accepted a new tool")
	}
	if err := saved.Provide("too-late", "value"); err == nil {
		t.Fatal("published generation accepted a new service")
	}
	_ = m.Stop(context.Background())
}
