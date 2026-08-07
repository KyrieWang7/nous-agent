package builtin

import (
	"context"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
)

const NameDynamicContext = "dynamicContext"

type DynamicContext struct {
	Text func(*middleware.State) string
}

func NewDynamicContext(fn func(*middleware.State) string) *DynamicContext {
	return &DynamicContext{Text: fn}
}
func (DynamicContext) Name() string { return NameDynamicContext }
func (d *DynamicContext) BeforeModel(_ context.Context, st *middleware.State) error {
	if d.Text == nil || st.ModelInput == nil {
		return nil
	}
	text := strings.TrimSpace(d.Text(st))
	if text == "" {
		return nil
	}
	st.ModelInput.Messages = append(st.ModelInput.Messages, message.Message{Role: message.RoleSystem, Content: "[Dynamic context]\n" + text})
	return nil
}
