package modelrouter_test

import (
	"context"
	"fmt"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/compaction"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/modelrouter"
	"strings"
	"testing"
)

func TestRecoveryPreservesTransientMessages(t *testing.T) {
	c, _ := compaction.New(compaction.Config{TriggerTokens: 100000, KeepMessages: 1}, compaction.NewModelSummariser(faux.New(faux.Text("summary"))))
	m := faux.New(faux.Fail(model.ErrContextOverflow), faux.Text("ok"))
	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{modelrouter.TierStandard: m}, Compactor: c})
	st := newState(user(strings.Repeat("old ", 500)), user("latest"))
	st.ModelInput.Messages = append(st.ModelInput.Messages, message.Message{Role: message.RoleSystem, Content: "unread inbox instructions"})
	if _, _, err := r.Sample(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	msgs := m.Requests()[1].Messages
	if len(msgs) != 3 || msgs[2].Content != "unread inbox instructions" {
		t.Fatalf("transient lost: %+v", msgs)
	}
}

func TestRoutedBudgetAndAttemptInputs(t *testing.T) {
	c, _ := compaction.New(compaction.Config{AutoBudget: true, KeepMessages: 1}, compaction.NewModelSummariser(faux.New(faux.Text("summary"))))
	large := faux.New(faux.Fail(model.ErrProviderUnavailable)).WithInfo(model.Info{Name: "large", ContextLength: 100000, MaxOutputTokens: 100})
	small := faux.New(faux.Text("ok")).WithInfo(model.Info{Name: "small", ContextLength: 1000, MaxOutputTokens: 600})
	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{modelrouter.TierStandard: large, modelrouter.TierFast: small}, Compactor: c})
	st := newState(user(strings.Repeat("old ", 350)), user("latest"))
	var inputs []model.Request
	var names []string
	st.BeforeModelAttempt = func(_ context.Context, info model.Info) error {
		inputs = append(inputs, *st.ModelInput)
		names = append(names, info.Name)
		return nil
	}
	if _, _, err := r.Sample(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || names[0] != "large" || names[1] != "small" || inputs[0].Messages[0].Content == inputs[1].Messages[0].Content || !st.Compacted {
		t.Fatalf("inputs=%+v names=%v", inputs, names)
	}
}
func TestAttemptCommitFailurePreventsProviderCall(t *testing.T) {
	m := faux.New(faux.Text("never"))
	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{modelrouter.TierStandard: m}})
	st := newState(user("hi"))
	st.BeforeModelAttempt = func(context.Context, model.Info) error { return fmt.Errorf("journal unavailable") }
	if _, _, err := r.Sample(context.Background(), st); err == nil || m.CallCount() != 0 {
		t.Fatalf("err=%v calls=%d", err, m.CallCount())
	}
}

func TestRecoveryDoesNotResurrectDuplicateHistory(t *testing.T) {
	c, _ := compaction.New(compaction.Config{TriggerTokens: 100000, KeepMessages: 1}, compaction.NewModelSummariser(faux.New(faux.Text("summary"))))
	m := faux.New(faux.Fail(model.ErrContextOverflow), faux.Text("ok"))
	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{modelrouter.TierStandard: m}, Compactor: c})
	st := newState(user("repeat"), user(strings.Repeat("long ", 500)), user("repeat"))
	st.ModelInput.Messages = append(st.ModelInput.Messages, message.Message{Role: message.RoleSystem, Content: "transient"})
	if _, _, err := r.Sample(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	msgs := m.Requests()[1].Messages
	if len(msgs) != 3 || msgs[1].Content != "repeat" || msgs[2].Content != "transient" {
		t.Fatalf("replayed=%+v", msgs)
	}
}

func TestRecoveryPreservesTransientMatchingTrimmedOutHistory(t *testing.T) {
	c, _ := compaction.New(compaction.Config{TriggerTokens: 100000, KeepMessages: 1}, compaction.NewModelSummariser(faux.New(faux.Text("summary"))))
	m := faux.New(faux.Fail(model.ErrContextOverflow), faux.Text("ok"))
	r := mustRouter(t, modelrouter.Config{Models: map[modelrouter.Tier]model.Model{modelrouter.TierStandard: m}, Compactor: c})
	old := user("temporarily needed again")
	long := user(strings.Repeat("long ", 500))
	latest := user("latest")
	st := newState(old, long, latest)
	st.ModelHistory = []message.Message{long, latest}
	st.ModelInput.Messages = []message.Message{old, long, latest}
	st.RebuildModelHistory = func() []message.Message { return st.History.All() }
	if _, _, err := r.Sample(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	msgs := m.Requests()[1].Messages
	if len(msgs) != 3 || msgs[0].Content != old.Content || msgs[2].Content != "latest" {
		t.Fatalf("transient lost or old history resurrected: %+v", msgs)
	}
}
