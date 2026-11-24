package ideas

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"

	model "github.com/KyrieWang7/nous-agent/manager/manager_service/internal/model/ideas"
)

var ErrNotFound = errors.New("选题不存在")

type Draft struct {
	Title  string
	Note   string
	Source string
	Status string
}

type Changes struct {
	Title  *string
	Note   string
	Source string
	Status *string
}

type Repository interface {
	List(context.Context) ([]model.Idea, error)
	Create(context.Context, model.Idea) (model.Idea, error)
	Update(context.Context, string, Changes) (model.Idea, bool, error)
	Delete(context.Context, string) (bool, error)
}

type Service struct {
	repository Repository
}

func New(repository Repository) *Service { return &Service{repository: repository} }

func (s *Service) List(ctx context.Context) ([]model.Idea, error) { return s.repository.List(ctx) }

func (s *Service) Create(ctx context.Context, draft Draft) (model.Idea, error) {
	var id [6]byte
	if _, err := rand.Read(id[:]); err != nil {
		return model.Idea{}, err
	}
	item := model.Idea{ID: hex.EncodeToString(id[:]), Title: strings.TrimSpace(draft.Title),
		Note: draft.Note, Source: draft.Source, Status: draft.Status}
	if item.Title == "" {
		item.Title = "未命名选题"
	}
	if !validStatus(item.Status) {
		item.Status = "pending"
	}
	return s.repository.Create(ctx, item)
}

func (s *Service) Update(ctx context.Context, id string, draft Draft) (model.Idea, error) {
	changes := Changes{Note: draft.Note, Source: draft.Source}
	if title := strings.TrimSpace(draft.Title); title != "" {
		changes.Title = &title
	}
	if validStatus(draft.Status) {
		changes.Status = &draft.Status
	}
	item, found, err := s.repository.Update(ctx, id, changes)
	if err == nil && !found {
		err = ErrNotFound
	}
	return item, err
}

func (s *Service) Delete(ctx context.Context, id string) error {
	found, err := s.repository.Delete(ctx, id)
	if err == nil && !found {
		err = ErrNotFound
	}
	return err
}

func validStatus(status string) bool {
	return status == "pending" || status == "doing" || status == "done"
}
