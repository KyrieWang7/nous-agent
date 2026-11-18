package service

import (
	"context"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

func (s *Service) GetQuestion(ctx context.Context, tid, rid, qid string) (runtime.QuestionRequest, error) {
	question, err := s.questions.Get(ctx, qid)
	if err != nil {
		return question, err
	}
	if question.ThreadID != tid || question.RunID != rid {
		return runtime.QuestionRequest{}, runtime.ErrQuestionNotFound
	}
	return question, nil
}
func (s *Service) AnswerQuestion(ctx context.Context, question runtime.QuestionRequest, answer runtime.QuestionAnswer, dismiss bool, by string) (runtime.QuestionRequest, error) {
	if strings.TrimSpace(by) == "" {
		by = "user"
	}
	if dismiss {
		return s.questions.Dismiss(ctx, question.ID, by)
	}
	if question.Status == runtime.QuestionExpired {
		return s.questions.AnswerLate(ctx, question.ID, answer, by)
	}
	return s.questions.Answer(ctx, question.ID, answer, by)
}
