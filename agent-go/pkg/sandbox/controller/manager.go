// Package controller owns remote sandbox identity, leases and reclamation.
package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
)

var ErrLease = errors.New("sandbox controller: invalid or expired lease")

type Enforcement struct {
	Backend        string `json:"backend"`
	Isolation      string `json:"isolation"`
	NetworkDefault string `json:"network_default"`
	NonRoot        bool   `json:"non_root"`
	ReadOnlyRoot   bool   `json:"read_only_root"`
	ResourceLimits bool   `json:"resource_limits"`
}

type Options struct {
	TTL, ReapInterval time.Duration
	Enforcement       Enforcement
}
type Lease struct {
	ID          string      `json:"id"`
	LeaseID     string      `json:"lease_id"`
	TenantID    string      `json:"tenant_id"`
	ThreadID    string      `json:"thread_id"`
	Root        string      `json:"root"`
	ExpiresAt   time.Time   `json:"expires_at"`
	Enforcement Enforcement `json:"enforcement"`
}
type record struct {
	Lease
	key    string
	handle sandbox.Handle
}
type Manager struct {
	provider  sandbox.Provider
	opts      Options
	mu        sync.Mutex
	byID      map[string]*record
	byKey     map[string]*record
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func New(provider sandbox.Provider, opts Options) (*Manager, error) {
	if provider == nil {
		return nil, errors.New("sandbox controller: provider is required")
	}
	enforced, ok := provider.(sandbox.EnforcementProvider)
	if !ok {
		return nil, errors.New("sandbox controller: backend must report runtime enforcement facts")
	}
	verifyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := enforced.Verify(verifyCtx); err != nil {
		return nil, fmt.Errorf("sandbox controller: backend verification failed: %w", err)
	}
	if opts.Enforcement.Backend == "" || opts.Enforcement.Isolation == "" || opts.Enforcement.NetworkDefault != "none" || !opts.Enforcement.NonRoot || !opts.Enforcement.ReadOnlyRoot || !opts.Enforcement.ResourceLimits {
		return nil, errors.New("sandbox controller: backend enforcement facts are incomplete; require isolated, network-none, non-root, read-only-root and resource limits")
	}
	if opts.TTL <= 0 {
		opts.TTL = 30 * time.Minute
	}
	if opts.ReapInterval <= 0 {
		opts.ReapInterval = min(opts.TTL/4, time.Minute)
	}
	m := &Manager{provider: provider, opts: opts, byID: map[string]*record{}, byKey: map[string]*record{}, stop: make(chan struct{}), done: make(chan struct{})}
	go m.reapLoop()
	return m, nil
}
func (m *Manager) Acquire(ctx context.Context, tenantID, threadID string) (Lease, error) {
	if tenantID == "" || threadID == "" {
		return Lease{}, errors.New("sandbox controller: tenant_id and thread_id are required")
	}
	key := tenantID + "\x00" + threadID
	now := time.Now().UTC()
	m.mu.Lock()
	if rec := m.byKey[key]; rec != nil && now.Before(rec.ExpiresAt) {
		rec.ExpiresAt = now.Add(m.opts.TTL)
		lease := rec.Lease
		m.mu.Unlock()
		return lease, nil
	}
	m.mu.Unlock()
	id, err := randomID("sbx-")
	if err != nil {
		return Lease{}, err
	}
	leaseID, err := randomID("lease-")
	if err != nil {
		return Lease{}, err
	}
	h, err := m.provider.Acquire(ctx, id)
	if err != nil {
		return Lease{}, fmt.Errorf("sandbox controller: backend acquire: %w", err)
	}
	rec := &record{Lease: Lease{ID: id, LeaseID: leaseID, TenantID: tenantID, ThreadID: threadID, Root: h.Root(), ExpiresAt: now.Add(m.opts.TTL), Enforcement: m.opts.Enforcement}, key: key, handle: h}
	m.mu.Lock()
	if existing := m.byKey[key]; existing != nil && now.Before(existing.ExpiresAt) {
		m.mu.Unlock()
		_ = m.provider.Release(ctx, id)
		return existing.Lease, nil
	}
	m.byID[id], m.byKey[key] = rec, rec
	m.mu.Unlock()
	return rec.Lease, nil
}
func (m *Manager) Use(id, leaseID string) (sandbox.Handle, Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	verified, ok := m.provider.(sandbox.EnforcementProvider)
	if !ok || !enforcementComplete(verified.Enforcement()) {
		return nil, Lease{}, errors.New("sandbox controller: backend enforcement is no longer verified")
	}
	rec := m.byID[id]
	if rec == nil || rec.LeaseID != leaseID || !time.Now().UTC().Before(rec.ExpiresAt) {
		return nil, Lease{}, ErrLease
	}
	rec.ExpiresAt = time.Now().UTC().Add(m.opts.TTL)
	return rec.handle, rec.Lease, nil
}

func enforcementComplete(facts map[string]bool) bool {
	for _, key := range []string{"isolated", "network_none", "non_root", "read_only_root", "resource_limits"} {
		if !facts[key] {
			return false
		}
	}
	return true
}
func (m *Manager) Status(id, leaseID string) (Lease, error) {
	_, lease, err := m.Use(id, leaseID)
	return lease, err
}
func (m *Manager) Release(ctx context.Context, id, leaseID string) error {
	m.mu.Lock()
	rec := m.byID[id]
	if rec == nil || rec.LeaseID != leaseID {
		m.mu.Unlock()
		return ErrLease
	}
	delete(m.byID, id)
	delete(m.byKey, rec.key)
	m.mu.Unlock()
	return m.provider.Release(ctx, id)
}
func (m *Manager) Enforcement() Enforcement { return m.opts.Enforcement }
func (m *Manager) Close() error             { m.closeOnce.Do(func() { close(m.stop) }); <-m.done; return nil }
func (m *Manager) reapLoop() {
	defer close(m.done)
	ticker := time.NewTicker(m.opts.ReapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case now := <-ticker.C:
			m.reap(now.UTC())
		}
	}
}
func (m *Manager) reap(now time.Time) {
	var expired []*record
	m.mu.Lock()
	for id, rec := range m.byID {
		if !now.Before(rec.ExpiresAt) {
			delete(m.byID, id)
			delete(m.byKey, rec.key)
			expired = append(expired, rec)
		}
	}
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, rec := range expired {
		_ = m.provider.Release(ctx, rec.ID)
	}
}
func randomID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}
