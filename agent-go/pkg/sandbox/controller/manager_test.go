package controller

import (
	"context"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
)

func enforcement() Enforcement {
	return Enforcement{Backend: "test", Isolation: "process", NetworkDefault: "none", NonRoot: true, ReadOnlyRoot: true, ResourceLimits: true}
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

type fakeProvider struct{ acquires, releases int }

func (p *fakeProvider) Acquire(_ context.Context, key string) (sandbox.Handle, error) {
	p.acquires++
	return fakeHandle{id: key}, nil
}
func (p *fakeProvider) Release(_ context.Context, _ string) error { p.releases++; return nil }
func (p *fakeProvider) Enforcement() map[string]bool {
	return map[string]bool{"isolated": true, "network_none": true, "non_root": true, "read_only_root": true, "resource_limits": true}
}
func (p *fakeProvider) Verify(context.Context) error { return nil }

type fakeHandle struct{ id string }

func (h fakeHandle) ID() string     { return h.id }
func (h fakeHandle) Root() string   { return "/mnt/user-data" }
func (h fakeHandle) FS() sandbox.FS { return nil }
func (h fakeHandle) Exec(context.Context, sandbox.Command) (*sandbox.ExecResult, error) {
	return nil, nil
}
