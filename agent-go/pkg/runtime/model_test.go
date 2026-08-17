package runtime_test

import (
	"context"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

func TestInstrumentModelAttributesAuxiliaryUsage(t *testing.T) {
	j := runtime.NewJournal(nil)
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{Journal: j})
	m := runtime.InstrumentModel(faux.New(faux.Text("ok").WithUsage(model.Usage{InputTokens: 4, OutputTokens: 1})), runtime.BucketAuxiliary, "title")
	if _, err := m.Complete(ctx, model.Request{}); err != nil {
		t.Fatal(err)
	}
	totals := j.Totals()
	if totals.AuxiliaryTokens != 5 || totals.LeadTokens != 0 {
		t.Fatalf("totals = %#v", totals)
	}
}
