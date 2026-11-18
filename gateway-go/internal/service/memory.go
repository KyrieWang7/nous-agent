package service

import (
	"context"
	"time"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/model"
)

func MemoryConfiguration() map[string]any {
	return map[string]any{"enabled": true, "storage_path": "postgresql", "debounce_seconds": 0, "max_facts": 50, "fact_confidence_threshold": 0.7, "injection_enabled": true, "max_injection_tokens": 1000}
}

func section() map[string]string { return map[string]string{"summary": "", "updatedAt": ""} }

func EmptyUser() map[string]any {
	return map[string]any{"workContext": section(), "personalContext": section(), "topOfMind": section()}
}

func EmptyHistory() map[string]any {
	return map[string]any{"recentMonths": section(), "earlierContext": section(), "longTermBackground": section()}
}

func (s *Service) Memory(ctx context.Context) (model.MemoryResponse, error) {
	facts, err := s.repository.Facts(ctx)
	if err != nil {
		return model.MemoryResponse{}, err
	}
	last := ""
	if len(facts) > 0 {
		last = facts[0].CreatedAt.UTC().Format(time.RFC3339)
	}
	return model.MemoryResponse{Version: "1.0", LastUpdated: last, User: EmptyUser(), History: EmptyHistory(), Facts: facts}, nil
}
func (s *Service) MemoryStatus(ctx context.Context) map[string]any {
	facts, _ := s.repository.Facts(ctx)
	return map[string]any{"config": MemoryConfiguration(), "data": model.MemoryResponse{Version: "1.0", User: EmptyUser(), History: EmptyHistory(), Facts: facts}}
}
