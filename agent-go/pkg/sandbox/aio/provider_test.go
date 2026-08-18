package aio

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProviderUsesExistingProvisionerAndAIOAPI(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		var body string
		switch r.URL.Path {
		case "/api/sandboxes":
			body = `{"sandbox_id":"sbx-1","sandbox_url":"http://sandbox-1:8080"}`
		case "/api/execute":
			body = `{"output":"ok"}`
		case "/api/read_file":
			body = `{"content":"hello"}`
		case "/api/list_dir":
			body = `{"entries":["file.txt"]}`
		case "/api/sandboxes/sbx-1":
			return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		default:
			t.Fatalf("unexpected %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	p, err := NewProvider(Options{ProvisionerURL: "http://provisioner", Client: client})
	if err != nil {
		t.Fatal(err)
	}
	h, err := p.Acquire(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx := sandbox.WithMode(context.Background(), sandbox.ModeWorkspaceWrite)
	result, err := h.Exec(ctx, sandbox.Command{Line: "echo ok"})
	if err != nil || result.Stdout != "ok" {
		t.Fatalf("exec=%#v err=%v", result, err)
	}
	data, err := h.FS().ReadFile(ctx, "/mnt/user-data/file.txt")
	if err != nil || string(data) != "hello" {
		t.Fatalf("read=%q err=%v", data, err)
	}
	if err := p.Release(context.Background(), "thread-1"); err != nil {
		t.Fatal(err)
	}
}

func TestProviderRejectsEscapingPath(t *testing.T) {
	p, _ := NewProvider(Options{ProvisionerURL: "http://provisioner"})
	h := &handle{provider: p, root: "/mnt/user-data"}
	fs := &remoteFS{handle: h}
	if _, err := fs.Resolve("/etc/passwd"); err != sandbox.ErrEscape {
		t.Fatalf("err=%v", err)
	}
}
