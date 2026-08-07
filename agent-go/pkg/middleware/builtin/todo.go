package builtin

import (
	"context"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
)

const NameTodo = "todo"

type Todo struct{ ToolName string }

func NewTodo() *Todo      { return &Todo{ToolName: "write_todos"} }
func (Todo) Name() string { return NameTodo }
func (t *Todo) BeforeModel(_ context.Context, st *middleware.State) error {
	enabled, _ := st.Value("is_plan_mode")
	if enabled == true {
		return nil
	}
	st.ToolSet = removeName(st.ToolSet, t.ToolName)
	if st.ModelInput != nil {
		out := st.ModelInput.Tools[:0]
		for _, schema := range st.ModelInput.Tools {
			if schema.Name != t.ToolName {
				out = append(out, schema)
			}
		}
		st.ModelInput.Tools = out
	}
	return nil
}
func removeName(in []string, name string) []string {
	out := in[:0]
	for _, v := range in {
		if v != name {
			out = append(out, v)
		}
	}
	return out
}
