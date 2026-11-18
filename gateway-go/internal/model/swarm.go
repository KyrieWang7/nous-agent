package model

type SwarmStatusEvent struct {
	AgentName string `json:"agent_name"`
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
}
