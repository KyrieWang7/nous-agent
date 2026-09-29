package compaction_test

import (
	"encoding/json"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/compaction"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"strings"
	"testing"
)

func TestRequestPressureIncludesSystemToolsAndHeadroom(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     compaction.Config
		req     model.Request
		info    model.Info
		want    bool
		wantErr bool
	}{
		{name: "system", cfg: compaction.Config{AutoBudget: true}, req: model.Request{System: strings.Repeat("x", 1300)}, info: model.Info{ContextLength: 1000, MaxOutputTokens: 600}, want: true},
		{name: "tool schema", cfg: compaction.Config{AutoBudget: true}, req: model.Request{Tools: []model.ToolSchema{{Name: "tool", Description: strings.Repeat("x", 1300), Parameters: json.RawMessage(`{}`)}}}, info: model.Info{ContextLength: 1000, MaxOutputTokens: 600}, want: true},
		{name: "headroom", cfg: compaction.Config{AutoBudget: true, HeadroomTokens: 400}, req: model.Request{System: strings.Repeat("x", 1300)}, info: model.Info{ContextLength: 1000, MaxOutputTokens: 200}, want: true},
		{name: "explicit override", cfg: compaction.Config{AutoBudget: true, TriggerTokens: 1000}, req: model.Request{System: strings.Repeat("x", 1300)}, info: model.Info{ContextLength: 1000, MaxOutputTokens: 600}},
		{name: "exhausted", cfg: compaction.Config{AutoBudget: true}, info: model.Info{ContextLength: 1000, MaxOutputTokens: 1000}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := compaction.New(tc.cfg, compaction.NewModelSummariser(faux.New()))
			got, err := c.ShouldCompactRequest(tc.req, tc.info)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("got=%v err=%v", got, err)
			}
		})
	}
}
