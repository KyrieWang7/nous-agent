package tool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func def(name string, opts ...func(*tool.Definition)) tool.Definition {
	d := tool.Definition{
		Name:        name,
		Group:       "test",
		Description: name + " description",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(context.Context, tool.Call) (*tool.Result, error) {
			return &tool.Result{Content: "ok"}, nil
		},
	}
	for _, o := range opts {
		o(&d)
	}
	return d
}

func readOnly(d *tool.Definition) {
	d.Metadata.IsReadOnly = true
	d.Metadata.IsConcurrencySafe = true
}

func deferred(d *tool.Definition) { d.Deferred = true }

func group(name string) func(*tool.Definition) {
	return func(d *tool.Definition) { d.Group = name }
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	if err := r.Register(def("ls", readOnly)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got, err := r.Get("ls")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "ls" {
		t.Fatalf("Get().Name = %q", got.Name)
	}
}

func TestRegistry_DuplicateRegisterFails(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	if err := r.Register(def("ls")); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if err := r.Register(def("ls")); err == nil {
		t.Fatal("duplicate Register() succeeded; must fail")
	}
}

func TestRegistry_RejectsInvalidDefinitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		def  tool.Definition
		want string
	}{
		{
			name: "empty name",
			def:  def(""),
			want: "name",
		},
		{
			name: "nil handler",
			def:  def("x", func(d *tool.Definition) { d.Handler = nil }),
			want: "handler",
		},
		{
			name: "empty group",
			def:  def("x", group("")),
			want: "group",
		},
		{
			name: "invalid parameters json",
			def:  def("x", func(d *tool.Definition) { d.Parameters = json.RawMessage(`{bad`) }),
			want: "parameters",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tool.NewRegistry().Register(tc.def)
			if err == nil {
				t.Fatalf("Register() succeeded, want error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// 设计文档 §7.3 硬约束：破坏性工具永不注册为可自动调用。
// 删除、退款确认、停用账号这类操作必须走人工审批路径，不进 agent 工具集。
func TestRegistry_RejectsDestructiveTools(t *testing.T) {
	t.Parallel()

	d := def("delete_everything", func(d *tool.Definition) { d.Metadata.Destructive = true })

	err := tool.NewRegistry().Register(d)
	if err == nil {
		t.Fatal("Register() accepted a destructive tool; §7.3 forbids it")
	}
	if !strings.Contains(err.Error(), "destructive") {
		t.Errorf("error = %q, want it to mention destructive", err)
	}
}

func TestRegistry_UnknownNameFailsFastAndListsAvailable(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	mustRegister(t, r, def("ls", readOnly))
	mustRegister(t, r, def("bash"))

	_, err := r.Get("grep")
	if err == nil {
		t.Fatal("Get() with unknown tool succeeded; must fail fast")
	}
	for _, want := range []string{"grep", "bash", "ls"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestRegistry_NamesSorted(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	mustRegister(t, r, def("zeta"))
	mustRegister(t, r, def("alpha"))
	mustRegister(t, r, def("mid"))

	got := r.Names()
	want := []string{"alpha", "mid", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names() = %v, want %v", got, want)
		}
	}
}

func TestRegistry_SchemasRespectsAllowList(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	mustRegister(t, r, def("ls", readOnly))
	mustRegister(t, r, def("bash"))
	mustRegister(t, r, def("write_file"))

	got := r.Schemas([]string{"ls", "bash"})
	if len(got) != 2 {
		t.Fatalf("Schemas() = %d entries (%v), want 2", len(got), schemaNames(got))
	}
	for _, s := range got {
		if s.Name == "write_file" {
			t.Errorf("Schemas() leaked a tool outside the allow list: %v", schemaNames(got))
		}
	}
}

// 延迟工具默认不披露，只有被已激活 skill 声明后才进请求体（设计文档 §7.3）。
func TestRegistry_SchemasExcludesDeferredByDefault(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	mustRegister(t, r, def("ls", readOnly))
	mustRegister(t, r, def("create_course_draft", deferred))

	got := r.Schemas([]string{"ls", "create_course_draft"})
	if len(got) != 1 || got[0].Name != "ls" {
		t.Fatalf("Schemas() = %v, want only [ls]; deferred tools must stay hidden", schemaNames(got))
	}
}

func TestRegistry_SchemasIncludesDeferredWhenDisclosed(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	mustRegister(t, r, def("ls", readOnly))
	mustRegister(t, r, def("create_course_draft", deferred))

	got := r.Schemas([]string{"ls", "create_course_draft"}, "create_course_draft")
	if len(got) != 2 {
		t.Fatalf("Schemas() = %v, want both tools once the deferred one is disclosed", schemaNames(got))
	}
}

// 披露一个不在白名单里的延迟工具不得放大权限：skill 只能披露已授权的工具。
func TestRegistry_DisclosureCannotEscapeAllowList(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	mustRegister(t, r, def("ls", readOnly))
	mustRegister(t, r, def("create_course_draft", deferred))

	got := r.Schemas([]string{"ls"}, "create_course_draft")
	if len(got) != 1 || got[0].Name != "ls" {
		t.Fatalf("Schemas() = %v; disclosure must not widen the allow list", schemaNames(got))
	}
}

func TestRegistry_SchemasIsStablyOrdered(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	mustRegister(t, r, def("zeta"))
	mustRegister(t, r, def("alpha"))

	// 稳定顺序对 prompt 缓存边界很重要：请求体逐字节稳定才能命中缓存
	first := schemaNames(r.Schemas([]string{"zeta", "alpha"}))
	second := schemaNames(r.Schemas([]string{"alpha", "zeta"}))

	if strings.Join(first, ",") != strings.Join(second, ",") {
		t.Fatalf("Schemas() order depends on the allow-list order: %v vs %v", first, second)
	}
}

func TestRegistry_NamesInGroup(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	mustRegister(t, r, def("ls", readOnly, group("file:read")))
	mustRegister(t, r, def("read_file", readOnly, group("file:read")))
	mustRegister(t, r, def("bash", group("bash")))

	got := r.NamesInGroup("file:read")
	if len(got) != 2 {
		t.Fatalf("NamesInGroup() = %v, want 2 entries", got)
	}
	if got[0] != "ls" || got[1] != "read_file" {
		t.Fatalf("NamesInGroup() = %v, want sorted [ls read_file]", got)
	}
}

func TestRegistry_ConcurrentReads(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	mustRegister(t, r, def("ls", readOnly))

	done := make(chan struct{})
	for range 20 {
		go func() {
			defer func() { done <- struct{}{} }()
			_, _ = r.Get("ls")
			_ = r.Names()
			_ = r.Schemas([]string{"ls"})
		}()
	}
	for range 20 {
		<-done
	}
}

func mustRegister(t *testing.T, r *tool.Registry, d tool.Definition) {
	t.Helper()
	if err := r.Register(d); err != nil {
		t.Fatalf("Register(%q) error = %v", d.Name, err)
	}
}

func schemaNames(schemas []model.ToolSchema) []string {
	out := make([]string, len(schemas))
	for i, s := range schemas {
		out[i] = s.Name
	}
	return out
}
