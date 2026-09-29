package modelrouter_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/compaction"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/modelrouter"
)

func TestSample_OverflowForcesCompactionBelowPressureThreshold(t *testing.T) {
	for _, keep := range []int{1, 10} {
		t.Run(strconv.Itoa(keep), func(t *testing.T) {
			summaryModel := faux.New(faux.Text("short summary"))
			c, err := compaction.New(compaction.Config{TriggerTokens: 1_000_000, KeepMessages: keep}, compaction.NewModelSummariser(summaryModel))
			if err != nil {
				t.Fatal(err)
			}
			std := faux.New(faux.Fail(model.ErrContextOverflow), faux.Text("fits now"))
			r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{modelrouter.TierStandard: std}, Compactor: c})
			st := newState(user(strings.Repeat("old context ", 400)), user(strings.Repeat("more old context ", 400)), user("latest question"))
			if c.ShouldCompact(st.History) {
				t.Fatal("test must begin below the proactive threshold")
			}
			resp, _, err := r.Sample(context.Background(), st)
			if err != nil {
				t.Fatalf("overflow recovery failed below threshold: %v", err)
			}
			if resp.Message.Content != "fits now" || !st.Compacted {
				t.Fatalf("response=%+v, compacted=%v", resp, st.Compacted)
			}
			reqs := std.Requests()
			if len(reqs) != 2 || len(reqs[1].Messages) != 2 || reqs[1].Messages[1].Content != "latest question" {
				t.Fatalf("retry did not use summary plus retained tail: %+v", reqs)
			}
		})
	}
}

func TestSample_OverflowDoesNotRetryAnExpandingSummary(t *testing.T) {
	summaryModel := faux.New(faux.Text(strings.Repeat("large summary ", 2000)))
	c, err := compaction.New(compaction.Config{TriggerTokens: 1_000_000, KeepMessages: 1}, compaction.NewModelSummariser(summaryModel))
	if err != nil {
		t.Fatal(err)
	}
	std := faux.New(faux.Fail(model.ErrContextOverflow), faux.Text("must not retry"))
	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{modelrouter.TierStandard: std}, Compactor: c})
	st := newState(user("old context"), user("latest question"))
	before := st.History.TokenCount()
	if _, _, err := r.Sample(context.Background(), st); !model.IsContextOverflow(err) {
		t.Fatalf("error=%v", err)
	}
	if summaryModel.CallCount() != 1 {
		t.Fatalf("summary calls=%d, want 1", summaryModel.CallCount())
	}
	if std.CallCount() != 1 || st.Compacted || st.History.TokenCount() != before {
		t.Fatal("non-shrinking summary was committed or retried")
	}
}
