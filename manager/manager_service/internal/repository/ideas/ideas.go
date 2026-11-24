package ideas

import (
	"context"

	model "github.com/KyrieWang7/nous-agent/manager/manager_service/internal/model/ideas"
	service "github.com/KyrieWang7/nous-agent/manager/manager_service/internal/service/ideas"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Repository { return &Repository{db: db} }

func (r *Repository) List(ctx context.Context) ([]model.Idea, error) {
	items := []model.Idea{}
	err := r.db.WithContext(ctx).Order("sequence DESC").Find(&items).Error
	return items, err
}

func (r *Repository) Create(ctx context.Context, item model.Idea) (model.Idea, error) {
	err := r.db.WithContext(ctx).Create(&item).Error
	return item, err
}

func (r *Repository) Update(ctx context.Context, id string, change service.Changes) (model.Idea, bool, error) {
	fields := map[string]any{"note": change.Note, "source": change.Source}
	if change.Title != nil {
		fields["title"] = *change.Title
	}
	if change.Status != nil {
		fields["status"] = *change.Status
	}
	var item model.Idea
	result := r.db.WithContext(ctx).Model(&item).Clauses(clause.Returning{}).Where("id = ?", id).Updates(fields)
	return item, result.RowsAffected > 0, result.Error
}

func (r *Repository) Delete(ctx context.Context, id string) (bool, error) {
	result := r.db.WithContext(ctx).Where("id = ?", id).Delete(&model.Idea{})
	return result.RowsAffected > 0, result.Error
}
