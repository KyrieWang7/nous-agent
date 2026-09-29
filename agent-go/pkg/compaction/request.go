package compaction

import (
	"context"
	"fmt"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

// MaybeCompactRequest uses the selected model and the complete request estimate.
// No per-run budget is stored on the shared compactor.
func (c *Compactor) MaybeCompactRequest(ctx context.Context, h *message.History, req model.Request, info model.Info) (bool, error) {
	if h == nil {
		return false, nil
	}
	needed, err := c.ShouldCompactRequest(req, info)
	if err != nil || !needed {
		return false, err
	}
	return c.compact(ctx, h, false, 0)
}

func (c *Compactor) ShouldCompactRequest(req model.Request, info model.Info) (bool, error) {
	trigger := c.cfg.TriggerTokens
	if trigger <= 0 {
		if !c.cfg.AutoBudget {
			return false, nil
		}
		output := info.MaxOutputTokens
		budget := info.ContextLength - max(0, output) - max(0, c.cfg.HeadroomTokens)
		if budget <= 0 {
			return false, fmt.Errorf("compaction: model %q leaves no message budget", info.Name)
		}
		trigger = max(1, budget*3/4)
	}
	tokens := message.EstimateMessagesTokens(req.Messages) + message.EstimateTokens(req.System)
	for _, t := range req.Tools {
		tokens += message.EstimateTokens(t.Name) + message.EstimateTokens(t.Description) + message.EstimateTokens(string(t.Parameters)) + 8
	}
	if tokens < trigger {
		return false, nil
	}
	return true, nil
}
