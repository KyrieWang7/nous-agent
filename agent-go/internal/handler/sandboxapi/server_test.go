package sandboxapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox/controller"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox/local"
)

type enforcedLocal struct{ sandbox.Provider }

func (enforcedLocal) Enforcement() map[string]bool {
	return map[string]bool{"isolated": true, "network_none": true, "non_root": true, "read_only_root": true, "resource_limits": true}
}
func (enforcedLocal) Verify(context.Context) error { return nil }

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	provider := enforcedLocal{Provider: local.NewProvider(local.Options{BaseDir: t.TempDir(), VirtualRoot: "/mnt/user-data"})}
	manager, err := controller.New(provider, controller.Options{TTL: time.Minute, ReapInterval: time.Hour, Enforcement: controller.Enforcement{Backend: "test", Isolation: "process", NetworkDefault: "none", NonRoot: true, ReadOnlyRoot: true, ResourceLimits: true}})
	if err != nil {
		t.Fatal(err)
	}
	api, err := New(manager, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api)
	t.Cleanup(func() { server.Close(); _ = manager.Close() })
	return server
}

func request(t *testing.T, server *httptest.Server, method, path string, body any, authorized bool) *http.Response {
	t.Helper()
	var raw bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&raw).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, server.URL+path, &raw)
	if err != nil {
		t.Fatal(err)
	}
	if authorized {
		req.Header.Set("Authorization", "Bearer test-secret")
	}
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func acquire(t *testing.T, server *httptest.Server) controller.Lease {
	t.Helper()
	response := request(t, server, http.MethodPost, "/v2/sandboxes/acquire", map[string]string{"tenant_id": "tenant", "thread_id": "thread"}, true)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("acquire status = %d", response.StatusCode)
	}
	var lease controller.Lease
	if err := json.NewDecoder(response.Body).Decode(&lease); err != nil {
		t.Fatal(err)
	}
	return lease
}

func TestServerRequiresBearerToken(t *testing.T) {
	server := newTestServer(t)
	response := request(t, server, http.MethodGet, "/v2/capabilities", nil, false)
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.StatusCode)
	}
}

func TestServerLifecycleAndFilesystem(t *testing.T) {
	server := newTestServer(t)
	lease := acquire(t, server)
	base := "/v2/sandboxes/" + lease.ID
	heartbeat := request(t, server, http.MethodPost, base+"/heartbeat", map[string]string{"lease_id": lease.LeaseID}, true)
	heartbeat.Body.Close()
	if heartbeat.StatusCode != http.StatusOK {
		t.Fatalf("heartbeat status = %d", heartbeat.StatusCode)
	}
	write := map[string]any{"lease_id": lease.LeaseID, "sandbox_mode": "workspace-write", "path": "/mnt/user-data/report.txt", "data_base64": base64.StdEncoding.EncodeToString([]byte("hello"))}
	response := request(t, server, http.MethodPost, base+"/fs/write", write, true)
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("write status = %d", response.StatusCode)
	}
	read := map[string]any{"lease_id": lease.LeaseID, "sandbox_mode": "read-only", "path": "/mnt/user-data/report.txt"}
	response = request(t, server, http.MethodPost, base+"/fs/read", read, true)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("read status = %d", response.StatusCode)
	}
	var result map[string]string
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	data, _ := base64.StdEncoding.DecodeString(result["data_base64"])
	if string(data) != "hello" {
		t.Fatalf("read = %q", data)
	}
}

func TestServerReadOnlyDeniesWriteAndInvalidLease(t *testing.T) {
	server := newTestServer(t)
	lease := acquire(t, server)
	path := "/v2/sandboxes/" + lease.ID + "/fs/write"
	body := map[string]any{"lease_id": lease.LeaseID, "sandbox_mode": "read-only", "path": "/mnt/user-data/file", "data_base64": ""}
	response := request(t, server, http.MethodPost, path, body, true)
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("read-only write status = %d", response.StatusCode)
	}
	body["lease_id"] = "wrong"
	response = request(t, server, http.MethodPost, path, body, true)
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("invalid lease status = %d", response.StatusCode)
	}
}
