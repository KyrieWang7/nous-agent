package handlers

import (
	"context"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
)

const NameTitle = "title"

type Title struct {
	model    model.Model
	maxWords int
	maxChars int
}

func NewTitle(m model.Model, maxWords, maxChars int) *Title {
	if maxWords <= 0 {
		maxWords = 8
	}
	if maxChars <= 0 {
		maxChars = 80
	}
	return &Title{model: m, maxWords: maxWords, maxChars: maxChars}
}
func (Title) Name() string           { return NameTitle }
func (Title) Grade() lifecycle.Grade { return lifecycle.GradeListener }
func (t *Title) AfterAgent(ctx context.Context, st *lifecycle.State) error {
	if t.model == nil || st.History == nil {
		return nil
	}
	if title, ok := st.Value("title"); ok && title != "" {
		return nil
	}
	msgs := st.History.All()
	var sample []message.Message
	for _, m := range msgs {
		if len(sample) == 0 && m.Role == message.RoleUser {
			sample = append(sample, m)
			continue
		}
		if len(sample) == 1 && m.Role == message.RoleAssistant && len(m.ToolCalls) == 0 && (strings.TrimSpace(m.Content) != "" || len(m.ContentBlocks) > 0) {
			sample = append(sample, m)
			break
		}
	}
	if len(sample) < 2 {
		return nil
	}
	resp, err := t.model.Complete(ctx, model.Request{System: "Create a concise conversation title. Return only the title, no quotes or punctuation commentary.", Messages: sample, MaxTokens: 32})
	if err != nil {
		return err
	}
	title := strings.TrimSpace(resp.Message.Content)
	words := strings.Fields(title)
	if len(words) > t.maxWords {
		title = strings.Join(words[:t.maxWords], " ")
	}
	if len(title) > t.maxChars {
		title = title[:t.maxChars]
	}
	st.SetValue("title", title)
	return nil
}
