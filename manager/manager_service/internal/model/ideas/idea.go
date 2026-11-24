package ideas

import "time"

type Idea struct {
	ID        string    `gorm:"column:id;primaryKey"`
	Sequence  int64     `gorm:"column:sequence;autoIncrement"`
	Title     string    `gorm:"column:title"`
	Note      string    `gorm:"column:note"`
	Source    string    `gorm:"column:source"`
	Status    string    `gorm:"column:status"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (Idea) TableName() string { return "manager_ideas" }
