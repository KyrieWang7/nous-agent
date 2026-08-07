package builtin

import (
	"context"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
)

const NameViewImage = "viewImage"

type ViewImage struct{ ToolName string }

func NewViewImage() *ViewImage { return &ViewImage{ToolName: "view_image"} }
func (ViewImage) Name() string { return NameViewImage }
func (v *ViewImage) BeforeModel(_ context.Context, st *middleware.State) error {
	supported, _ := st.Value("supports_vision")
	if supported == true {
		injectViewedImages(st)
		return nil
	}
	st.ToolSet = removeName(st.ToolSet, v.ToolName)
	if st.ModelInput != nil {
		out := st.ModelInput.Tools[:0]
		for _, schema := range st.ModelInput.Tools {
			if schema.Name != v.ToolName {
				out = append(out, schema)
			}
		}
		st.ModelInput.Tools = out
	}
	return nil
}

func injectViewedImages(st *middleware.State) {
	if st.History == nil || st.ModelInput == nil {
		return
	}
	msgs := st.History.All()
	assistant := -1
	callIDs := map[string]bool{}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != message.RoleAssistant {
			continue
		}
		for _, call := range msgs[i].ToolCalls {
			if call.Name == "view_image" {
				callIDs[call.ID] = true
			}
		}
		assistant = i
		break
	}
	if assistant < 0 || len(callIDs) == 0 {
		return
	}
	var blocks []message.ContentBlock
	for _, msg := range msgs[assistant+1:] {
		if msg.Role == message.RoleTool && callIDs[msg.ToolCallID] {
			blocks = append(blocks, msg.ContentBlocks...)
		}
	}
	if len(blocks) > 0 {
		st.ModelInput.Messages = append(st.ModelInput.Messages, message.Message{Role: message.RoleUser, Content: "Here are the images you viewed.", ContentBlocks: blocks})
	}
}
