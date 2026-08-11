package remote

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestRemoteProviderLifecycleFilesAndExec(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer test" {
			t.Fatalf("missing authorization header")
		}
		switch request.URL.Path {
		case "/v1/sandboxes/thread-1/acquire":
			return remoteResponse(200, `{"id":"sandbox-1","root":"/mnt/user-data"}`), nil
		case "/v1/sandboxes/sandbox-1/fs/read":
			return remoteResponse(200, `{"data_base64":"`+base64.StdEncoding.EncodeToString([]byte("hello"))+`"}`), nil
		case "/v1/sandboxes/sandbox-1/fs/list":
			return remoteResponse(200, `{"entries":[{"name":"file.txt","is_dir":false,"size":5,"mode":"-rw-r--r--"}]}`), nil
		case "/v1/sandboxes/sandbox-1/exec":
			return remoteResponse(200, `{"stdout":"ok","stderr":"","exit_code":0,"timed_out":false,"truncated":false}`), nil
		case "/v1/sandboxes/thread-1":
			return remoteResponse(204, ""), nil
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})}
	provider, err := NewProvider(Options{BaseURL: "https://sandbox.test", Headers: map[string]string{"Authorization": "Bearer test"}, Client: client})
	if err != nil {
		t.Fatal(err)
	}
	handle, err := provider.Acquire(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := handle.FS().ReadFile(context.Background(), "/mnt/user-data/file.txt")
	if err != nil || string(data) != "hello" {
		t.Fatalf("read = %q, err = %v", data, err)
	}
	entries, err := handle.FS().List(context.Background(), "/mnt/user-data")
	if err != nil || len(entries) != 1 || entries[0].IsDir || entries[0].Size != 5 {
		t.Fatalf("entries = %#v, err = %v", entries, err)
	}
	result, err := handle.Exec(context.Background(), sandbox.Command{Line: "echo ok"})
	if err != nil || result.Stdout != "ok" || result.ExitCode != 0 {
		t.Fatalf("exec = %#v, err = %v", result, err)
	}
	if err := provider.Release(context.Background(), "thread-1"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteFSRejectsEscapingPath(t *testing.T) {
	provider, _ := NewProvider(Options{BaseURL: "https://sandbox.test"})
	h := &handle{provider: provider, id: "sandbox-1", root: "/mnt/user-data"}
	fsys := &remoteFS{handle: h}
	if _, err := fsys.Resolve("/etc/passwd"); err != sandbox.ErrEscape {
		t.Fatalf("error = %v", err)
	}
}

func remoteResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
