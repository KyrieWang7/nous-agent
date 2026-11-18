package service

import (
	"strings"
	"time"

	domain "github.com/KyrieWang7/nous-agent/gateway-go/internal/model"
)

func SameMembers(left, right []domain.Member) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Name != right[i].Name || left[i].Status != right[i].Status ||
			!sameOptionalString(left[i].Model, right[i].Model) ||
			!sameOptionalTime(left[i].JoinedAt, right[i].JoinedAt) {
			return false
		}
	}
	return true
}

func sameOptionalString(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func sameOptionalTime(left, right *time.Time) bool {
	return left == nil && right == nil || left != nil && right != nil && left.Equal(*right)
}

func ChangedMemberStatuses(previous, current []domain.Member, suppressed map[string]struct{}) []domain.SwarmStatusEvent {
	previousStatuses := make(map[string]string, len(previous))
	for _, member := range previous {
		previousStatuses[member.Name] = member.Status
	}
	events := make([]domain.SwarmStatusEvent, 0)
	for _, member := range current {
		if _, ok := suppressed[member.Name]; ok {
			continue
		}
		if previousStatuses[member.Name] == member.Status || !frontendSwarmStatus(member.Status) {
			continue
		}
		events = append(events, domain.SwarmStatusEvent{AgentName: member.Name, Status: member.Status})
	}
	return events
}

func StatusFromSwarmMessage(message domain.Message) (domain.SwarmStatusEvent, bool) {
	if message.From != "system" {
		return domain.SwarmStatusEvent{}, false
	}
	prefixes := []struct {
		prefix string
		status string
	}{
		{prefix: "[Joined]", status: "active"},
		{prefix: "[Completed]", status: "completed"},
		{prefix: "[Failed]", status: "failed"},
		{prefix: "[Timeout]", status: "timeout"},
	}
	for _, candidate := range prefixes {
		if !strings.HasPrefix(message.Content, candidate.prefix) {
			continue
		}
		detail := strings.TrimSpace(strings.TrimPrefix(message.Content, candidate.prefix))
		name := detail
		if candidate.status == "active" {
			name, _, _ = strings.Cut(name, " started working on:")
		}
		name, _, _ = strings.Cut(name, ":")
		name = strings.TrimSpace(name)
		if name == "" {
			return domain.SwarmStatusEvent{}, false
		}
		return domain.SwarmStatusEvent{AgentName: name, Status: candidate.status, Detail: message.Content}, true
	}
	return domain.SwarmStatusEvent{}, false
}

func frontendSwarmStatus(status string) bool {
	switch status {
	case "active", "running", "completed", "failed", "timeout":
		return true
	default:
		return false
	}
}
