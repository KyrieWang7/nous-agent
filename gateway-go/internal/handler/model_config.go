package handler

import (
	"errors"
	"net/http"

	"github.com/KyrieWang7/nous-agent/platform-go/modelconfig"
)

func (s *Handler) getModelSettings(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	settings, err := s.app.ModelSettings()
	if err != nil {
		problem(w, 500, errors.New("could not read model settings"))
		return
	}
	writeJSON(w, 200, settings)
}

func (s *Handler) putModelSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	input, err := modelconfig.Decode(r.Body)
	if err != nil {
		problem(w, 400, err)
		return
	}
	settings, err := s.app.SaveModelSettings(input)
	var validation *modelconfig.ValidationError
	if errors.As(err, &validation) {
		problem(w, 400, validation)
		return
	}
	if err != nil {
		problem(w, 500, errors.New("could not save model settings"))
		return
	}
	writeJSON(w, 200, settings)
}
