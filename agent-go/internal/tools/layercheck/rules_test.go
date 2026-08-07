package main

import (
	"strings"
	"testing"
)

func TestCheck_FlagsReverseDependency(t *testing.T) {
	t.Parallel()

	deps := map[string][]string{
		"pkg/tool": {"pkg/model", "pkg/middleware"}, // 违规：工具层反向依赖中间件
	}

	got := check(deps)
	if len(got) != 1 {
		t.Fatalf("check() = %d violations, want 1: %v", len(got), got)
	}
	if got[0].dep != "pkg/middleware" {
		t.Errorf("violation dep = %q, want pkg/middleware", got[0].dep)
	}
	if !strings.Contains(got[0].why, "成环") {
		t.Errorf("violation why = %q, want it to explain the cycle", got[0].why)
	}
}

func TestCheck_AllowsLegalDependencies(t *testing.T) {
	t.Parallel()

	deps := map[string][]string{
		"pkg/message":             nil,
		"pkg/model":               {"pkg/message"},
		"pkg/tool":                {"pkg/message", "pkg/model"},
		"pkg/middleware":          {"pkg/message", "pkg/model", "pkg/tool"},
		"pkg/loop":                {"pkg/message", "pkg/model", "pkg/tool", "pkg/middleware"},
		"pkg/harness":             {"pkg/loop", "pkg/middleware", "pkg/tool", "pkg/model", "pkg/message"},
		"pkg/model/provider/faux": {"pkg/message", "pkg/model"},
		"internal/langgraphapi":   {"pkg/loop", "pkg/harness"},
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
		{name: "wire format", dep: "internal/langgraphapi"},
		{name: "harness", dep: "pkg/harness"},
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

func TestCheck_LibraryMustNotDependOnInternal(t *testing.T) {
	t.Parallel()

	got := check(map[string][]string{"pkg/storage": {"internal/langgraphapi"}})
	if len(got) == 0 {
		t.Fatal("check() allowed pkg/* → internal/*")
	}
}

func TestCheck_ContractMustNotDependOnItsImplementations(t *testing.T) {
	t.Parallel()

	got := check(map[string][]string{"pkg/middleware": {"pkg/middleware/builtin"}})
	if len(got) == 0 {
		t.Fatal("check() allowed pkg/middleware → pkg/middleware/builtin")
	}

	// 但实现本身依赖契约是合法的
	if got := check(map[string][]string{"pkg/middleware/builtin": {"pkg/middleware"}}); len(got) != 0 {
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
