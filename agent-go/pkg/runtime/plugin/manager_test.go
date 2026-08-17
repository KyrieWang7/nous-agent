package plugin_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/plugin"
)

type testPlugin struct {
	name string
	deps []string
	log  *[]string
	fail bool
}

func (p testPlugin) Name() string           { return p.name }
func (p testPlugin) Dependencies() []string { return p.deps }
func (p testPlugin) Start(context.Context, plugin.Host) error {
	*p.log = append(*p.log, "start:"+p.name)
	if p.fail {
		return context.DeadlineExceeded
	}
	return nil
}
func (p testPlugin) Stop(context.Context) error {
	*p.log = append(*p.log, "stop:"+p.name)
	return nil
}

func TestManagerStartsDependenciesAndStopsReverse(t *testing.T) {
	var log []string
	m, err := plugin.NewManager([]plugin.Plugin{
		testPlugin{name: "consumer", deps: []string{"provider"}, log: &log},
		testPlugin{name: "provider", log: &log},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background(), plugin.Host{Capabilities: capability.NewRegistry()}); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"start:provider", "start:consumer", "stop:consumer", "stop:provider"}
	if !reflect.DeepEqual(log, want) {
		t.Fatalf("lifecycle = %v, want %v", log, want)
	}
}

func TestManagerRejectsMissingDependencyAndRollsBackPartialStart(t *testing.T) {
	var log []string
	if _, err := plugin.NewManager([]plugin.Plugin{testPlugin{name: "consumer", deps: []string{"missing"}, log: &log}}); err == nil {
		t.Fatal("expected missing dependency error")
	}
	m, err := plugin.NewManager([]plugin.Plugin{testPlugin{name: "ok", log: &log}, testPlugin{name: "bad", deps: []string{"ok"}, log: &log, fail: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background(), plugin.Host{Capabilities: capability.NewRegistry()}); err == nil {
		t.Fatal("expected start failure")
	}
	want := []string{"start:ok", "start:bad", "stop:ok"}
	if !reflect.DeepEqual(log, want) {
		t.Fatalf("rollback lifecycle = %v, want %v", log, want)
	}
}
