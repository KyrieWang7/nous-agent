package guardrail

import (
	"context"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
)

func TestModelProviderParsesStructuredDecision(t *testing.T) {
	provider, err := NewModelProvider(faux.New(faux.Text("```json\n{\"action\":\"block\",\"reason\":\"unsafe\",\"risk_level\":\"high\"}\n```")))
	if err != nil {
		t.Fatal(err)
	}
	decision, err := provider.Evaluate(context.Background(), DirectionInput, "bad request")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != ActionBlock || decision.RiskLevel != "high" {
		t.Fatalf("decision = %#v", decision)
	}
}
