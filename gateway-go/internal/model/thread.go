package model

import (
	"errors"
	"time"
)

type Thread struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	ModelName    string    `json:"model_name"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	IsArchived   bool      `json:"is_archived"`
	MessageCount int64     `json:"message_count"`
}

type ThreadPatch struct {
	Title      *string
	ModelName  *string
	IsArchived *bool
}

type Fact struct {
	ID         string    `json:"id"`
	Content    string    `json:"content"`
	Category   string    `json:"category"`
	Confidence float64   `json:"confidence"`
	CreatedAt  time.Time `json:"createdAt"`
	Source     string    `json:"source"`
}

type Team struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type Member struct {
	Name     string     `json:"name"`
	Status   string     `json:"status"`
	Model    *string    `json:"model"`
	JoinedAt *time.Time `json:"joined_at"`
}

type Message struct {
	ID        int64     `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type Schedule struct {
	ID           string         `json:"id"`
	ThreadID     string         `json:"thread_id"`
	Name         string         `json:"name"`
	ScheduleType string         `json:"schedule_type"`
	Expression   string         `json:"expression"`
	Timezone     string         `json:"timezone"`
	Input        map[string]any `json:"input"`
	Enabled      bool           `json:"enabled"`
	NextRunAt    time.Time      `json:"next_run_at"`
	LastRunAt    *time.Time     `json:"last_run_at,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type SchedulePatch struct {
	Name         *string
	ScheduleType *string
	Expression   *string
	Timezone     *string
	Input        map[string]any
	Enabled      *bool
	NextRunAt    *time.Time
}

var ErrNotFound = errors.New("not found")
