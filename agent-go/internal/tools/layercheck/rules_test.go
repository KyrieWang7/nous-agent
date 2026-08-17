package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectRemovedDirectories(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	removed := filepath.Join(root, "pkg", "middleware")
	if err := os.MkdirAll(removed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := rejectRemovedDirectories(removed); err == nil {
		t.Fatal("removed architecture directory was accepted")
	}
}

func TestRejectRemovedTransportHarness(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "harness.go")
	if err := os.WriteFile(path, []byte("package httpapi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rejectRemovedFiles(path); err == nil {
		t.Fatal("removed transport harness was accepted")
	}
}

func TestCheck_FlagsReverseDependency(t *testing.T) {
	t.Parallel()

	deps := map[string][]string{
		"pkg/tool": {"pkg/model", "pkg/runtime/lifecycle"}, // 违规：工具层反向依赖内核运行周期
	}

	got := check(deps)
	if len(got) != 1 {
		t.Fatalf("check() = %d violations, want 1: %v", len(got), got)
	}
	if got[0].dep != "pkg/runtime/lifecycle" {
		t.Errorf("violation dep = %q, want pkg/runtime/lifecycle", got[0].dep)
	}
	if !strings.Contains(got[0].why, "成环") {
		t.Errorf("violation why = %q, want it to explain the cycle", got[0].why)
	}
}

func TestCheck_AllowsLegalDependencies(t *testing.T) {
	t.Parallel()

	deps := map[string][]string{
		"pkg/message":                nil,
		"pkg/model":                  {"pkg/message"},
		"pkg/tool":                   {"pkg/message", "pkg/model"},
		"pkg/runtime/lifecycle":      {"pkg/message", "pkg/model", "pkg/tool"},
		"pkg/loop":                   {"pkg/message", "pkg/model", "pkg/tool", "pkg/runtime/lifecycle"},
		"pkg/harness":                {"pkg/loop", "pkg/runtime/lifecycle", "pkg/tool", "pkg/model", "pkg/message"},
		"pkg/model/provider/faux":    {"pkg/message", "pkg/model"},
		"internal/transport/httpapi": {"pkg/runtime/runmanager", "pkg/message"},
	}

	if got := check(deps); len(got) != 0 {
		t.Fatalf("check() reported violations on a legal graph: %v", got)
	}
}

func TestCheck_KernelMustNotDependOnConfigOrWireFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dep  string
	}{
		{name: "config", dep: "pkg/config"},
		{name: "wire format", dep: "internal/transport/httpapi"},
		{name: "harness", dep: "pkg/harness"},
		{name: "postgres adapter", dep: "pkg/runtime/postgres"},
		{name: "redis adapter", dep: "pkg/runtime/redis"},
		{name: "model provider", dep: "pkg/model/provider/openai"},
		{name: "tool implementation", dep: "pkg/tool/builtin"},
		{name: "sandbox implementation", dep: "pkg/sandbox/local"},
		{name: "HTTP transport", dep: "external/net/http"},
		{name: "SQL driver", dep: "external/database/sql"},
		{name: "pgx driver", dep: "external/github.com/jackc/pgx/v5"},
		{name: "Redis driver", dep: "external/github.com/redis/go-redis/v9"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := check(map[string][]string{"pkg/loop": {tc.dep}})
			if len(got) == 0 {
				t.Fatalf("check() allowed pkg/loop → %s", tc.dep)
			}
		})
	}
}

func TestCheckTransportAndRuntimeBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		from string
		dep  string
	}{
		{from: "internal/transport/httpapi", dep: "pkg/loop"},
		{from: "internal/transport/httpapi", dep: "pkg/model/provider/openai"},
		{from: "pkg/runtime/runmanager", dep: "internal/transport/httpapi"},
		{from: "pkg/runtime/runmanager", dep: "pkg/runtime/postgres"},
		{from: "pkg/runtime/capability", dep: "pkg/loop"},
	}
	for _, tc := range tests {
		if got := check(map[string][]string{tc.from: {tc.dep}}); len(got) == 0 {
			t.Errorf("check() allowed %s -> %s", tc.from, tc.dep)
		}
	}
}

func TestCheck_LibraryMustNotDependOnInternal(t *testing.T) {
	t.Parallel()

	got := check(map[string][]string{"pkg/storage": {"internal/transport/httpapi"}})
	if len(got) == 0 {
		t.Fatal("check() allowed pkg/* → internal/*")
	}
}

func TestCheck_ContractMustNotDependOnItsImplementations(t *testing.T) {
	t.Parallel()

	got := check(map[string][]string{"pkg/runtime/lifecycle": {"pkg/runtime/lifecycle/handlers"}})
	if len(got) == 0 {
		t.Fatal("check() allowed lifecycle contract → builtin implementation")
	}

	// 但实现本身依赖契约是合法的
	if got := check(map[string][]string{"pkg/runtime/lifecycle/handlers": {"pkg/runtime/lifecycle"}}); len(got) != 0 {
		t.Fatalf("check() flagged the legal builtin → contract direction: %v", got)
	}
}

func TestCheck_SubpackagesAreAllowed(t *testing.T) {
	t.Parallel()

	// pkg/tool 依赖 pkg/tool/builtin 不是违规
	if got := check(map[string][]string{"pkg/tool": {"pkg/tool/builtin"}}); len(got) != 0 {
		t.Fatalf("check() flagged a package depending on its own subpackage: %v", got)
	}
}

func TestCheck_SandboxMustNotGrowPermissionLogic(t *testing.T) {
	t.Parallel()

	got := check(map[string][]string{"pkg/sandbox": {"pkg/permission"}})
	if len(got) == 0 {
		t.Fatal("check() allowed pkg/sandbox → pkg/permission; the sandbox must stay isolation-only")
	}
}

// 路径段比较而非字符串前缀：否则 pkg/tooling 会被误判为 pkg/tool 的子包。
func TestUnderPath_UsesPathSegments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		p, prefix string
		want      bool
	}{
		{"pkg/tool", "pkg/tool", true},
		{"pkg/tool/builtin", "pkg/tool", true},
		{"pkg/tooling", "pkg/tool", false},
		{"pkg/toolkit/x", "pkg/tool", false},
		{"pkg/tool", "", false},
	}

	for _, tc := range tests {
		if got := underPath(tc.p, tc.prefix); got != tc.want {
			t.Errorf("underPath(%q, %q) = %v, want %v", tc.p, tc.prefix, got, tc.want)
		}
	}
}

func TestRelative(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{modulePath, "."},
		{modulePath + "/pkg/loop", "pkg/loop"},
		{"github.com/other/repo/pkg/loop", ""},
		{"context", ""},
	}

	for _, tc := range tests {
		if got := relative(tc.in); got != tc.want {
			t.Errorf("relative(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
