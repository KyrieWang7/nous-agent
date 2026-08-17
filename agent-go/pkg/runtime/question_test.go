package runtime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

func TestQuestionManagerRequiresOneKnownAnswerAndKeepsFirstTerminalState(t *testing.T) {
	manager, err := runtime.NewQuestionManager(runtime.NewMemoryQuestionStore())
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.Create(context.Background(), runtime.QuestionRequest{
		ID: "question-1", RunID: "run-1", Header: "Plan review", Question: "Approve?",
		Options: []runtime.QuestionOption{{Label: "Approve"}, {Label: "Keep planning"}},
	})
	if err != nil || created.Status != runtime.QuestionPending {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	if _, err := manager.Answer(context.Background(), created.ID, runtime.QuestionAnswer{Selected: []string{"unknown"}}, "user"); err == nil {
		t.Fatal("unknown option was accepted")
	}
	answered, err := manager.Answer(context.Background(), created.ID, runtime.QuestionAnswer{Selected: []string{"Keep planning"}, Custom: "cover rollback"}, "user")
	if err != nil {
		t.Fatal(err)
	}
	if answered.Status != runtime.QuestionAnswered || answered.Answer.Custom != "cover rollback" {
		t.Fatalf("answered=%#v", answered)
	}
	second, err := manager.Answer(context.Background(), created.ID, runtime.QuestionAnswer{Selected: []string{"Approve"}}, "other")
	if err != nil {
		t.Fatal(err)
	}
	if second.Answer.Selected[0] != "Keep planning" || second.AnsweredBy != "user" {
		t.Fatalf("terminal answer was overwritten: %#v", second)
	}
}

func TestQuestionManagerCreateIsIdempotentButReportsExisting(t *testing.T) {
	manager, err := runtime.NewQuestionManager(runtime.NewMemoryQuestionStore())
	if err != nil {
		t.Fatal(err)
	}
	request := runtime.QuestionRequest{
		ID: "question-1", RunID: "run-1", Header: "Review", Question: "Continue?",
		Options: []runtime.QuestionOption{{Label: "Yes"}},
	}
	if _, err := manager.Create(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	existing, err := manager.Create(context.Background(), request)
	if !errors.Is(err, runtime.ErrQuestionExists) || existing.ID != request.ID {
		t.Fatalf("existing=%#v err=%v", existing, err)
	}
}
