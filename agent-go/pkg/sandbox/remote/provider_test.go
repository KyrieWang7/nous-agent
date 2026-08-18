package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestAcquireHeartbeatsCachedLease(t *testing.T) {
	var mu sync.Mutex
	acquires, heartbeats := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v2/sandboxes/acquire":
			acquires++
			_ = json.NewEncoder(w).Encode(acquireResponse{ID: "sbx-1", LeaseID: "lease-1", Root: "/mnt/user-data"})
		case "/v2/sandboxes/sbx-1/heartbeat":
			heartbeats++
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p, err := NewProvider(Options{BaseURL: server.URL, TenantID: "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	one, err := p.Acquire(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	two, err := p.Acquire(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if one != two || acquires != 1 || heartbeats != 1 {
		t.Fatalf("one=%p two=%p acquires=%d heartbeats=%d", one, two, acquires, heartbeats)
	}
}

func TestAcquireReplacesExpiredCachedLease(t *testing.T) {
	var mu sync.Mutex
	acquires := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v2/sandboxes/acquire":
			acquires++
			_ = json.NewEncoder(w).Encode(acquireResponse{ID: "sbx-" + string(rune('0'+acquires)), LeaseID: "lease-new", Root: "/mnt/user-data"})
		case "/v2/sandboxes/sbx-1/heartbeat":
			http.Error(w, `{"error":"sandbox controller: invalid or expired lease"}`, http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p, err := NewProvider(Options{BaseURL: server.URL, TenantID: "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	one, err := p.Acquire(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	two, err := p.Acquire(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if one.ID() != "sbx-1" || two.ID() != "sbx-2" || acquires != 2 {
		t.Fatalf("old=%s new=%s acquires=%d", one.ID(), two.ID(), acquires)
	}
}
