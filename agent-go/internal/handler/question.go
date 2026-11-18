package handler

import (
	"errors"
	"net/http"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

func (s *Handler) getQuestion(w http.ResponseWriter, r *http.Request) {
	question, err := s.app.GetQuestion(r.Context(), r.PathValue("tid"), r.PathValue("rid"), r.PathValue("qid"))
	if err != nil {
		if errors.Is(err, runtime.ErrQuestionNotFound) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, question)
}

func (s *Handler) answerQuestion(w http.ResponseWriter, r *http.Request) {
	question, err := s.app.GetQuestion(r.Context(), r.PathValue("tid"), r.PathValue("rid"), r.PathValue("qid"))
	if err != nil {
		if errors.Is(err, runtime.ErrQuestionNotFound) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	var body struct {
		Selected []string `json:"selected"`
		Custom   string   `json:"custom"`
		Dismiss  bool     `json:"dismiss"`
		By       string   `json:"by"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	question, err = s.app.AnswerQuestion(r.Context(), question, runtime.QuestionAnswer{Selected: body.Selected, Custom: body.Custom}, body.Dismiss, body.By)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if question.Status == runtime.QuestionLateAnswered {
		writeJSON(w, http.StatusOK, map[string]any{"question": question, "continuation_required": true})
		return
	}
	writeJSON(w, http.StatusOK, question)
}
