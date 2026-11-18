package service

import (
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/model"
)

func (s *Service) Models() ([]model.Model, error) { return s.catalog.Models() }

func (s *Service) Extensions() (model.Extensions, error) { return s.catalog.Extensions() }

func (s *Service) SaveMCP(servers map[string]model.MCPServer) (model.Extensions, error) {
	return s.catalog.SaveMCP(servers)
}

func (s *Service) Skills() ([]model.Skill, error) {
	items, err := s.catalog.Skills()
	for i := range items {
		items[i].Content = ""
	}
	return items, err
}

func (s *Service) Skill(name string) (model.Skill, error) { return s.catalog.Skill(name) }

func (s *Service) SetSkillEnabled(name string, enabled bool) (model.Skill, error) {
	item, err := s.catalog.SetSkillEnabled(name, enabled)
	item.Content = ""
	return item, err
}
func (s *Service) Model(name string) (model.Model, error) {
	items, err := s.catalog.Models()
	if err != nil {
		return model.Model{}, err
	}
	for _, item := range items {
		if item.Name == name {
			return item, nil
		}
	}
	return model.Model{}, model.ErrModelNotFound
}
