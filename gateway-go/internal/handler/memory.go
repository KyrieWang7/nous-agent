package handler

import (
	"net/http"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/service"
)

func (s *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "healthy", "service": "nous-gateway-go", "database": s.app.Available()})
}

func (s *Handler) memory(w http.ResponseWriter, r *http.Request) {
	data, err := s.app.Memory(r.Context())
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, data)
}

func (s *Handler) memoryConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, service.MemoryConfiguration())
}

func (s *Handler) memoryStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.app.MemoryStatus(r.Context()))
}
