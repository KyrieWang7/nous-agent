package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/swarm"
)

const NameInboxPoller = "inboxPoller"

type InboxPoller struct {
	mailbox swarm.Mailbox
	limit   int
}

func NewInboxPoller(mailbox swarm.Mailbox, limit int) *InboxPoller {
	return &InboxPoller{mailbox: mailbox, limit: limit}
}
func (InboxPoller) Name() string            { return NameInboxPoller }
func (InboxPoller) Grade() middleware.Grade { return middleware.GradeListener }
func (p *InboxPoller) BeforeModel(ctx context.Context, st *middleware.State) error {
	if p.mailbox == nil || st.ModelInput == nil {
		return nil
	}
	var (
		msgs []swarm.Message
		err  error
	)
	if trusted, ok := p.mailbox.(swarm.ThreadMailbox); ok {
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
