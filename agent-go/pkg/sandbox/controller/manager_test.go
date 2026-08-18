package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
)

func enforcement() Enforcement {
	return Enforcement{Backend: "test", Isolation: "process", NetworkDefault: "none", NonRoot: true, ReadOnlyRoot: true, ResourceLimits: true}
}

func TestManagerReconcilesBackendAtStartup(t *testing.T) {
	p := &reconcilingProvider{}
	m, err := New(p, Options{Enforcement: enforcement()})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if p.reconciles != 1 {
		t.Fatalf("reconciles=%d", p.reconciles)
	}
}

func TestManagerRetriesFailedReclamation(t *testing.T) {
	p := &fakeProvider{releaseErr: errors.New("temporary docker failure")}
	m, err := New(p, Options{TTL: time.Minute, ReapInterval: time.Hour, Enforcement: enforcement()})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	lease, err := m.Acquire(context.Background(), "tenant-a", "thread-reap")
	if err != nil {
		t.Fatal(err)
	}
	m.reap(lease.ExpiresAt)
	if p.releases != 1 || m.byID[lease.ID] == nil {
		t.Fatalf("failed reclamation was forgotten: releases=%d record=%v", p.releases, m.byID[lease.ID])
	}
	p.releaseErr = nil
	m.reap(lease.ExpiresAt.Add(time.Second))
	if p.releases != 2 || m.byID[lease.ID] != nil {
		t.Fatalf("reclamation was not retried: releases=%d record=%v", p.releases, m.byID[lease.ID])
	}
}

func TestManagerRetriesFailedExplicitRelease(t *testing.T) {
	p := &fakeProvider{releaseErr: errors.New("temporary docker failure")}
	m, err := New(p, Options{TTL: time.Minute, ReapInterval: time.Hour, Enforcement: enforcement()})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	lease, err := m.Acquire(context.Background(), "tenant-a", "thread-release")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Release(context.Background(), lease.ID, lease.LeaseID); err == nil {
		t.Fatal("release failure was hidden")
	}
	if m.byID[lease.ID] == nil {
		t.Fatal("failed release discarded the lease")
	}
	p.releaseErr = nil
	if err := m.Release(context.Background(), lease.ID, lease.LeaseID); err != nil {
		t.Fatal(err)
	}
	if p.releases != 2 || m.byID[lease.ID] != nil {
		t.Fatalf("release was not retried: releases=%d record=%v", p.releases, m.byID[lease.ID])
	}
}

func TestManagerRejectsUnverifiedBackend(t *testing.T) {
	if _, err := New(&fakeProvider{}, Options{Enforcement: Enforcement{Backend: "docker"}}); err == nil {
		t.Fatal("incomplete enforcement accepted")
	}
}

func TestManagerTenantThreadIdempotencyAndLease(t *testing.T) {
	p := &fakeProvider{}
	m, err := New(p, Options{TTL: time.Minute, ReapInterval: time.Hour, Enforcement: enforcement()})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	one, err := m.Acquire(context.Background(), "tenant-a", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	two, err := m.Acquire(context.Background(), "tenant-a", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if one.ID != two.ID || p.acquires != 1 {
		t.Fatalf("not idempotent: %#v %#v acquires=%d", one, two, p.acquires)
	}
	if _, _, err := m.Use(one.ID, "wrong"); err == nil {
		t.Fatal("wrong lease accepted")
	}
	if _, _, err := m.Use(one.ID, one.LeaseID); err != nil {
		t.Fatal(err)
	}
	if err := m.Release(context.Background(), one.ID, one.LeaseID); err != nil {
		t.Fatal(err)
	}
	if p.releases != 1 {
		t.Fatalf("releases=%d", p.releases)
	}
}

func TestManagerSeparatesResourceAndWorkspaceIdentity(t *testing.T) {
	p := &workspaceProvider{fakeProvider: fakeProvider{}}
	m, err := New(p, Options{TTL: time.Minute, ReapInterval: time.Hour, Enforcement: enforcement()})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	lease, err := m.Acquire(context.Background(), "tenant-a", "thread-stable")
	if err != nil {
		t.Fatal(err)
	}
	if p.resourceKey != lease.ID {
		t.Fatalf("resource key = %q, want lease ID %q", p.resourceKey, lease.ID)
	}
	if p.workspaceKey != "thread-stable" {
		t.Fatalf("workspace key = %q, want thread-stable", p.workspaceKey)
	}
}

type fakeProvider struct {
	acquires, releases int
	releaseErr         error
}

func (p *fakeProvider) Acquire(_ context.Context, key string) (sandbox.Handle, error) {
	p.acquires++
	return fakeHandle{id: key}, nil
}
func (p *fakeProvider) Release(_ context.Context, _ string) error {
	p.releases++
	return p.releaseErr
}
func (p *fakeProvider) Enforcement() map[string]bool {
	return map[string]bool{"isolated": true, "network_none": true, "non_root": true, "read_only_root": true, "resource_limits": true}
}
func (p *fakeProvider) Verify(context.Context) error { return nil }

type workspaceProvider struct {
	fakeProvider
	resourceKey  string
	workspaceKey string
}

func (p *workspaceProvider) AcquireWorkspace(_ context.Context, resourceKey, workspaceKey string) (sandbox.Handle, error) {
	p.acquires++
	p.resourceKey = resourceKey
	p.workspaceKey = workspaceKey
	return fakeHandle{id: resourceKey}, nil
}

type reconcilingProvider struct {
	fakeProvider
	reconciles int
}

func (p *reconcilingProvider) Reconcile(context.Context) error {
	p.reconciles++
	return nil
}

type fakeHandle struct{ id string }

func (h fakeHandle) ID() string     { return h.id }
func (h fakeHandle) Root() string   { return "/mnt/user-data" }
func (h fakeHandle) FS() sandbox.FS { return nil }
func (h fakeHandle) Exec(context.Context, sandbox.Command) (*sandbox.ExecResult, error) {
	return nil, nil
}
