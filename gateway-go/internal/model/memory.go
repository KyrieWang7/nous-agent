package model

type MemoryResponse struct {
	Version     string         `json:"version"`
	LastUpdated string         `json:"lastUpdated"`
	User        map[string]any `json:"user"`
	History     map[string]any `json:"history"`
	Facts       []Fact         `json:"facts"`
}
