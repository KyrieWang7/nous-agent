package service

import (
	"context"
	"time"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/model"
)

type StreamOptions struct {
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
}

// StreamSwarm polls domain state and emits changes. It is independent of HTTP,
// including cursor handling, member diffing and terminal team deletion.
func StreamSwarm(ctx context.Context, queries SwarmQueries, teamID string, after int64, opts StreamOptions, yield func(model.StreamEvent) error) error {
	if opts.PollInterval <= 0 {
		opts.PollInterval = time.Second
	}
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = 15 * time.Second
	}
	send := func(name string, id int64, data any) error {
		return yield(model.StreamEvent{Name: name, ID: id, Data: data})
	}
	sendError := func(err error) error { return send("error", 0, map[string]string{"error": err.Error()}) }
	deleted := func() error { return send("team_deleted", 0, map[string]string{"team_id": teamID}) }
	exists, err := queries.TeamExists(ctx, teamID)
	if err != nil {
		return sendError(err)
	}
	if !exists {
		return deleted()
	}
	members, err := queries.Members(ctx, teamID)
	if err != nil {
		return sendError(err)
	}
	if err = send("team_update", 0, members); err != nil {
		return err
	}
	ticker := time.NewTicker(opts.PollInterval)
	heartbeat := time.NewTicker(opts.HeartbeatInterval)
	defer ticker.Stop()
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-heartbeat.C:
			if err := send("heartbeat", 0, map[string]string{"timestamp": now.UTC().Format(time.RFC3339)}); err != nil {
				return err
			}
		case <-ticker.C:
			exists, err := queries.TeamExists(ctx, teamID)
			if err != nil {
				if err = sendError(err); err != nil {
					return err
				}
				continue
			}
			if !exists {
				return deleted()
			}
			messageStatuses := make(map[string]struct{})
			messages, err := queries.Messages(ctx, teamID, after, 100)
			if err != nil {
				if err = sendError(err); err != nil {
					return err
				}
			} else {
				for _, message := range messages {
					after = message.ID
					if err = send("message", message.ID, message); err != nil {
						return err
					}
					if event, ok := StatusFromSwarmMessage(message); ok {
						if err = send("status", 0, event); err != nil {
							return err
						}
						messageStatuses[event.AgentName] = struct{}{}
					}
				}
			}
			current, err := queries.Members(ctx, teamID)
			if err != nil {
				if err = sendError(err); err != nil {
					return err
				}
				continue
			}
			if !SameMembers(members, current) {
				statuses := ChangedMemberStatuses(members, current, messageStatuses)
				members = current
				if err = send("team_update", 0, members); err != nil {
					return err
				}
				for _, status := range statuses {
					if err = send("status", 0, status); err != nil {
						return err
					}
				}
			}
		}
	}
}
