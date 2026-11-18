package subagent

import (
	"context"
	"errors"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

var (
	ErrOutputLimit = errors.New("subagent: output token limit reached; result may be partial")
	ErrRefused     = errors.New("subagent: delegated input was refused")
)

// Execution outcome is independent of successful resource release. A stopped
// or truncated child must never become a successful task just because Run
// returned no infrastructure error.
func childStopReason(result *loop.Result, err error) (string, error) {
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return "aborted", err
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, loop.ErrDeadline) {
			return "timed-out", err
		}
		return "error", err
	}
	if result != nil {
		switch result.EndReason {
		case "max-tokens":
			return "max-tokens", ErrOutputLimit
		case "blocked":
			return "refusal", ErrRefused
		}
		if result.StopReason == model.StopReasonContentFilter {
			return "refusal", ErrRefused
		}
	}
	return "completed", nil
}

// Like DSH finalAssistantOutput, empty usage-only assistant messages do not
// erase earlier output. The caller supplies only child-owned transcript input.
func finalAssistantOutput(messages []message.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != message.RoleAssistant {
			continue
		}
		if msg.Content != "" {
			return msg.Content
		}
		text := ""
		for _, block := range msg.ContentBlocks {
			if block.Type == "text" {
				text += block.Text
			}
		}
		if text != "" {
			return text
		}
	}
	return ""
}
