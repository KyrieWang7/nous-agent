package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/config"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/harness"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/plugin"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/plugincontrol"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func TestPluginControlPersistsAndDrainsRealRegistrations(t *testing.T) {
	isolateReloadEnvironment(t)
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	root := filepath.Join(dir, "plugins")
	pkg := filepath.Join(root, "validation")
	if err := os.MkdirAll(pkg, 0750); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"name":"validation","description":"local validation plugin","tools":[{"name":"validation_echo","command":"printf PLUGIN_OK","requiredSandboxMode":"read-only"}]}`)
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	writeReloadConfig(t, path, "test")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fmt.Fprintf(file, "plugins:\n  enabled: true\n  directories: [%q]\n", root); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var closes atomic.Int32
	var failNext atomic.Bool
	var failCloseNext atomic.Bool
	var registries []*tool.Registry
	build := func(c config.Config) (builtAgent, error) {
		registry := tool.NewRegistry()
		registries = append(registries, registry)
		var contributions []plugin.Contribution
		var err error
		if c.Plugins.Enabled {
			contributions, err = plugin.Load(c.Plugins.Directories, nil, c.Plugins.States)
			if err != nil {
				return builtAgent{}, err
			}
		}
		h, err := harness.New(harness.Options{Model: faux.New(faux.Text("unused")), Registry: registry, Plugins: commandToolPlugins(contributions)})
		if err != nil {
			return builtAgent{}, err
		}
		failClose := failCloseNext.Swap(false)
		b := builtAgent{agent: harness.Agent{Runner: h.Runner()}, tools: registry.Names(), close: []func() error{func() error {
			closes.Add(1)
			err := h.Close()
			if failClose {
				return errors.Join(err, errors.New("old plugin cleanup failed"))
			}
			return err
		}}}
		for _, c := range contributions {
			if !slices.Contains(b.plugins, c.PluginName) {
				b.plugins = append(b.plugins, c.PluginName)
			}
		}
		if failNext.Swap(false) {
			return b, errors.New("candidate assembly failed")
		}
		return b, nil
	}
	initial, err := build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	reloader, err := newReloadableAgent(path, cfg, initial, build)
	if err != nil {
		t.Fatal(err)
	}
	defer reloader.Close()
	first, err := reloader.PrepareRun()
	if err != nil {
		t.Fatal(err)
	}
	before, err := reloader.Snapshot(ctx)
	if err != nil || len(before.Plugins) != 1 || !before.Plugins[0].Loaded {
		t.Fatalf("snapshot=%+v err=%v", before, err)
	}
	off, err := reloader.Apply(ctx, plugincontrol.Change{Name: "validation", Enabled: false, Revision: before.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if off.Plugins[0].Loaded || off.Plugins[0].Enabled || off.Plugins[0].RetainedRuns != 1 {
		t.Fatalf("disable=%+v", off)
	}
	if !slices.Contains(registries[0].Names(), "validation_echo") {
		t.Fatal("disabled an in-flight generation")
	}
	next, err := reloader.PrepareRun()
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(next.AllowedTools, "validation_echo") {
		t.Fatal("new run retained disabled tool")
	}
	next.Release()
	first.Release()
	first.Release() // leases are idempotent, including after retirement
	if len(registries[0].Names()) != 0 || closes.Load() != 1 {
		t.Fatalf("retired registrations=%v closes=%d", registries[0].Names(), closes.Load())
	}
	if _, err = reloader.Apply(ctx, plugincontrol.Change{Name: "validation", Enabled: true, Revision: before.Revision}); !errors.Is(err, plugincontrol.ErrConflict) {
		t.Fatalf("stale revision=%v", err)
	}
	failNext.Store(true)
	if _, err = reloader.Apply(ctx, plugincontrol.Change{Name: "validation", Enabled: true, Revision: off.Revision}); err == nil {
		t.Fatal("failed candidate was published")
	}
	state, err := config.ReadPluginState(config.PluginStatePath())
	if err != nil || state.Plugins["validation"] {
		t.Fatalf("failed candidate persisted: %+v err=%v", state, err)
	}
	if len(registries[len(registries)-1].Names()) != 0 {
		t.Fatal("failed candidate leaked tool registrations")
	}
	snapshot, err := reloader.Snapshot(ctx)
	if err != nil || snapshot.Plugins[0].RetainedRuns != 0 || snapshot.Plugins[0].Loaded {
		t.Fatalf("after drain=%+v err=%v", snapshot, err)
	}
	// A separately assembled worker observes durable settings, not process flags.
	cfg, err = config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	initial, err = build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newReloadableAgent(path, cfg, initial, build)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	restarted, err := second.Snapshot(ctx)
	if err != nil || restarted.Plugins[0].Enabled || restarted.Plugins[0].Loaded {
		t.Fatalf("restart=%+v err=%v", restarted, err)
	}
	failCloseNext.Store(true)
	enabled, err := reloader.Apply(ctx, plugincontrol.Change{Name: "validation", Enabled: true, Revision: snapshot.Revision})
	if err != nil || !enabled.Plugins[0].Loaded {
		t.Fatalf("enable=%+v err=%v", enabled, err)
	}
	if _, err = second.Apply(ctx, plugincontrol.Change{Name: "validation", Enabled: false, Revision: restarted.Revision}); !errors.Is(err, plugincontrol.ErrConflict) {
		t.Fatalf("cross-worker stale update=%v", err)
	}
	observed, err := second.Snapshot(ctx)
	if err != nil || !observed.Plugins[0].Loaded {
		t.Fatalf("cross-worker reload=%+v err=%v", observed, err)
	}
	disabled, err := reloader.Apply(ctx, plugincontrol.Change{Name: "validation", Enabled: false, Revision: enabled.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Plugins[0].CloseError == "" {
		t.Fatal("old generation cleanup failure was hidden")
	}
	info, err := os.Stat(config.PluginStatePath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("settings permissions=%v err=%v", info, err)
	}
}
