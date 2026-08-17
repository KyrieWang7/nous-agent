package handlers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/swarm"
)

const NameInboxPoller = "inboxPoller"

type InboxPoller struct {
	mailbox  swarm.Mailbox
	limit    int
	interval time.Duration
	now      func() time.Time
	lastPoll sync.Map
}

func NewInboxPoller(mailbox swarm.Mailbox, limit int, interval ...time.Duration) *InboxPoller {
	pollInterval := 2 * time.Second
	if len(interval) > 0 && interval[0] > 0 {
		pollInterval = interval[0]
	}
	return &InboxPoller{mailbox: mailbox, limit: limit, interval: pollInterval, now: time.Now}
}
func (*InboxPoller) Name() string           { return NameInboxPoller }
func (*InboxPoller) Grade() lifecycle.Grade { return lifecycle.GradeListener }
func (p *InboxPoller) BeforeModel(ctx context.Context, st *lifecycle.State) error {
	if p.mailbox == nil || st.ModelInput == nil {
		return nil
	}
	// A registered mailbox is a server capability, not permission to consume
	// messages. Disabled runs must leave receipts untouched for a later Swarm
	// run that actually owns them.
	enabled, _ := st.Value("swarm_enabled")
	if value, ok := enabled.(bool); !ok || !value {
		return nil
	}
	if st.RunID != "" && p.interval > 0 {
		now := p.now()
		if last, ok := p.lastPoll.Load(st.RunID); ok && now.Sub(last.(time.Time)) < p.interval {
			return nil
		}
		p.lastPoll.Store(st.RunID, now)
	}
	var (
		msgs []swarm.Message
		err  error
	)
	if run, ok := runtime.RunContextFrom(ctx); ok && run.SwarmTeamID != "" && run.SwarmAgentName != "" {
		msgs, err = p.mailbox.Poll(ctx, run.SwarmTeamID, run.SwarmAgentName, p.limit)
	} else if trusted, ok := p.mailbox.(swarm.ThreadMailbox); ok {
		msgs, err = trusted.PollForThread(ctx, st.ThreadID, p.limit)
	} else {
		team, _ := st.Value("swarm_team_id")
		agent, _ := st.Value("swarm_agent_name")
		teamID, _ := team.(string)
		agentName, _ := agent.(string)
		if teamID == "" || agentName == "" {
			return nil
		}
		msgs, err = p.mailbox.Poll(ctx, teamID, agentName, p.limit)
	}
	if err != nil {
		// A thread that is not in a swarm is the normal case.
		if strings.Contains(err.Error(), "not a member") {
			return nil
		}
		return err
	}
	if len(msgs) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("New teammate messages:\n")
	for _, msg := range msgs {
		fmt.Fprintf(&b, "- from %s: %s\n", msg.From, msg.Content)
	}
	st.ModelInput.Messages = append(st.ModelInput.Messages, message.Message{Role: message.RoleSystem, Content: strings.TrimSpace(b.String())})
	return nil
}

func (p *InboxPoller) AfterAgent(_ context.Context, st *lifecycle.State) error {
	if p != nil && st != nil && st.RunID != "" {
		p.lastPoll.Delete(st.RunID)
	}
	return nil
}
