// Package guardrail defines structured input/output safety decisions.
package guardrail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

type Action string

const (
	ActionPass   Action = "pass"
	ActionReview Action = "review"
	ActionBlock  Action = "block"
)

func (a Action) Valid() bool { return a == ActionPass || a == ActionReview || a == ActionBlock }

type Direction string

const (
	DirectionInput  Direction = "input"
	DirectionOutput Direction = "output"
)

type Decision struct {
	Action      Action `json:"action"`
	Reason      string `json:"reason,omitempty"`
	Replacement string `json:"replacement,omitempty"`
	RiskLevel   string `json:"risk_level,omitempty"`
}
type Provider interface {
	Evaluate(context.Context, Direction, string) (Decision, error)
}
type Evaluator struct {
	provider   Provider
	failClosed bool
}

func New(provider Provider, failClosed bool) (*Evaluator, error) {
	if provider == nil {
		return nil, errors.New("guardrail: provider is nil")
	}
	return &Evaluator{provider: provider, failClosed: failClosed}, nil
}
func (e *Evaluator) Evaluate(ctx context.Context, direction Direction, text string) (Decision, error) {
	decision, err := e.provider.Evaluate(ctx, direction, text)
	if err != nil {
		if e.failClosed {
			return Decision{Action: ActionBlock, Reason: "guardrail provider unavailable", RiskLevel: "unknown"}, nil
		}
		return Decision{Action: ActionReview, Reason: err.Error(), RiskLevel: "unknown"}, nil
	}
	if !decision.Action.Valid() {
		if e.failClosed {
			return Decision{Action: ActionBlock, Reason: "guardrail returned an invalid decision", RiskLevel: "unknown"}, nil
		}
		return Decision{}, fmt.Errorf("guardrail: invalid action %q", decision.Action)
	}
	return decision, nil
}

type ProviderFunc func(context.Context, Direction, string) (Decision, error)

func (f ProviderFunc) Evaluate(ctx context.Context, d Direction, s string) (Decision, error) {
	return f(ctx, d, s)
}

// ModelProvider asks a model for the structured safety decision. Deployments
// can replace it with a dedicated policy service through Provider.
type ModelProvider struct{ model model.Model }

func NewModelProvider(m model.Model) (*ModelProvider, error) {
	if m == nil {
		return nil, errors.New("guardrail: model provider requires a model")
	}
	return &ModelProvider{model: m}, nil
}

func (p *ModelProvider) Evaluate(ctx context.Context, direction Direction, text string) (Decision, error) {
	resp, err := p.model.Complete(ctx, model.Request{
		System:    "You are a content safety classifier. Return only JSON with action (pass, review, or block), reason, replacement, and risk_level. Block requests or responses that facilitate clearly harmful or illegal activity; otherwise pass.",
		Messages:  []message.Message{{Role: message.RoleUser, Content: fmt.Sprintf("direction: %s\ncontent:\n%s", direction, text)}},
		MaxTokens: 256,
	})
	if err != nil {
		return Decision{}, err
	}
	if resp == nil {
		return Decision{}, errors.New("guardrail: model returned no response")
	}
	raw := strings.TrimSpace(resp.Message.Content)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	var decision Decision
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &decision); err != nil {
		return Decision{}, fmt.Errorf("guardrail: decoding model decision: %w", err)
	}
	return decision, nil
}
