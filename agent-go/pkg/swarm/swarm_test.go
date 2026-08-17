package swarm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
	"github.com/pashagolub/pgxmock/v4"
)

func TestBroadcastIsStoredOnceForGatewayAndPerRecipientDelivery(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	db.ExpectExec("INSERT INTO agent_swarm_messages").
		WithArgs("team-1", "alice", "*", "hello").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := manager.Send(context.Background(), "team-1", "alice", "*", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAddMemberRejectsInvalidNamesBeforeDatabaseAccess(t *testing.T) {
	t.Parallel()

	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"bad name",
		"*",
		"-worker",
		".worker",
		"System",
		"LEAD",
		strings.Repeat("a", 65),
		"line\nbreak",
	} {
		if err := manager.AddMember(context.Background(), Member{TeamID: "team-1", Name: name}); err == nil {
			t.Errorf("AddMember(%q) succeeded", name)
		}
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPollDeliversEligibleBroadcastOncePerRecipient(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	pollQuery := "m.to_agent='\\*' AND m.from_agent<>\\$2 AND EXISTS.*recipient.joined_at<=m.created_at"
	db.ExpectBegin()
	db.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs("team-1", "bob").WillReturnResult(pgxmock.NewResult("SELECT", 1))
	db.ExpectQuery(pollQuery).WithArgs("team-1", "bob", 50).WillReturnRows(
		pgxmock.NewRows([]string{"id", "team_id", "from_agent", "to_agent", "content", "created_at"}).
			AddRow(int64(1), "team-1", "alice", "*", "hello", time.Now().UTC()),
	)
	db.ExpectExec("INSERT INTO agent_swarm_message_receipts").WithArgs([]int64{1}, "bob").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	db.ExpectCommit()

	messages, err := manager.Poll(context.Background(), "team-1", "bob", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].To != "*" {
		t.Fatalf("messages = %#v", messages)
	}

	db.ExpectBegin()
	db.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs("team-1", "bob").WillReturnResult(pgxmock.NewResult("SELECT", 1))
	db.ExpectQuery(pollQuery).WithArgs("team-1", "bob", 50).WillReturnRows(
		pgxmock.NewRows([]string{"id", "team_id", "from_agent", "to_agent", "content", "created_at"}),
	)
	db.ExpectCommit()

	messages, err = manager.Poll(context.Background(), "team-1", "bob", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("second poll returned an acknowledged message: %#v", messages)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPollDoesNotAcknowledgeMessagesAfterRowError(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	db.ExpectBegin()
	db.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs("team-1", "alice").WillReturnResult(pgxmock.NewResult("SELECT", 1))
	db.ExpectQuery("SELECT m.id").WithArgs("team-1", "alice", 50).WillReturnRows(
		pgxmock.NewRows([]string{"id", "team_id", "from_agent", "to_agent", "content", "created_at"}).
			AddRow(int64(1), "team-1", "lead", "alice", "hello", time.Now().UTC()).
			AddRow(int64(2), "team-1", "lead", "alice", "world", time.Now().UTC()).
			RowError(1, context.Canceled),
	)
	db.ExpectRollback()

	if _, err := manager.Poll(context.Background(), "team-1", "alice", 50); err == nil {
		t.Fatal("Poll() succeeded after a row iteration error")
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPollLeadMailbox(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	db.ExpectBegin()
	db.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs("team-1", LeadAgentName).WillReturnResult(pgxmock.NewResult("SELECT", 1))
	db.ExpectQuery("SELECT m.id").WithArgs("team-1", LeadAgentName, 50).WillReturnRows(
		pgxmock.NewRows([]string{"id", "team_id", "from_agent", "to_agent", "content", "created_at"}).
			AddRow(int64(1), "team-1", "reviewer", LeadAgentName, "done", time.Now().UTC()),
	)
	db.ExpectExec("INSERT INTO agent_swarm_message_receipts").WithArgs([]int64{1}, LeadAgentName).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	db.ExpectCommit()

	messages, err := manager.Poll(context.Background(), "team-1", LeadAgentName, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].To != LeadAgentName {
		t.Fatalf("messages = %#v", messages)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSendMessageDerivesSenderFromRunThread(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	db.ExpectQuery("SELECT team_id,agent_name").WithArgs("thread-alice", "team-1", LeadAgentName).WillReturnRows(
		pgxmock.NewRows([]string{"team_id", "agent_name"}).AddRow("team-1", "alice"),
	)
	db.ExpectQuery("SELECT EXISTS").WithArgs("team-1", "bob", LeadAgentName).WillReturnRows(
		pgxmock.NewRows([]string{"exists"}).AddRow(true),
	)
	db.ExpectExec("INSERT INTO agent_swarm_messages").WithArgs("team-1", "alice", "bob", "hello").WillReturnResult(pgxmock.NewResult("INSERT", 1))

	var send tool.Definition
	for _, definition := range manager.Tools() {
		if definition.Name == "send_message" {
			send = definition
			break
		}
	}
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{RunID: "run-1", ThreadID: "thread-alice"})
	result, err := send.Handler(ctx, tool.Call{Args: []byte(`{"team_id":"team-1","from":"mallory","to":"bob","content":"hello"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("result = %#v", result)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSendMessagePrefersTrustedChildIdentity(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	db.ExpectQuery("SELECT EXISTS").WithArgs("team-1", "bob", LeadAgentName).WillReturnRows(
		pgxmock.NewRows([]string{"exists"}).AddRow(true),
	)
	db.ExpectExec("INSERT INTO agent_swarm_messages").WithArgs("team-1", "reviewer", "bob", "done").WillReturnResult(pgxmock.NewResult("INSERT", 1))

	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{
		RunID: "parent:task-1", ThreadID: "lead-thread",
		SwarmTeamID: "team-1", SwarmAgentName: "reviewer",
	})
	result, err := findTool(t, manager, "send_message").Handler(ctx, tool.Call{Args: []byte(`{"team_id":"team-1","to":"bob","content":"done"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("result = %#v", result)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSendMessageToolAcceptsPythonTeamLeadRecipient(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	db.ExpectQuery("SELECT EXISTS").WithArgs("team-1", LeadAgentName, LeadAgentName).WillReturnRows(
		pgxmock.NewRows([]string{"exists"}).AddRow(true),
	)
	db.ExpectExec("INSERT INTO agent_swarm_messages").
		WithArgs("team-1", "reviewer", LeadAgentName, "done").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{
		RunID: "parent:task-1", ThreadID: "lead-thread",
		SwarmTeamID: "team-1", SwarmAgentName: "reviewer",
	})
	result, err := findTool(t, manager, "send_message").Handler(ctx, tool.Call{Args: []byte(`{"to":"lead","content":"done"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("result = %#v", result)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSendMessageRejectsChildTeamSpoofing(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{
		RunID: "parent:task-1", ThreadID: "lead-thread",
		SwarmTeamID: "team-1", SwarmAgentName: "reviewer",
	})
	result, err := findTool(t, manager, "send_message").Handler(ctx, tool.Call{Args: []byte(`{"team_id":"team-2","to":"bob","content":"forged"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content, "does not match the trusted child identity") {
		t.Fatalf("result = %#v", result)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestChildCannotChangeTeamLifecycle(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{
		ThreadID: "lead-thread", SwarmTeamID: "team-1", SwarmAgentName: "reviewer",
	})
	result, err := findTool(t, manager, "team_create").Handler(ctx, tool.Call{Args: []byte(`{"name":"forged"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content, "only the lead") {
		t.Fatalf("result = %#v", result)
	}
}

func TestManagementToolSchemasDoNotAcceptIdentityFields(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	create := findTool(t, manager, "team_create")
	deleteTool := findTool(t, manager, "team_delete")
	for _, definition := range []tool.Definition{create, deleteTool} {
		schema := string(definition.Parameters)
		for _, forbidden := range []string{"thread_id", "lead_thread_id", `"from"`} {
			if strings.Contains(schema, forbidden) {
				t.Fatalf("%s schema exposes trusted identity field %q: %s", definition.Name, forbidden, schema)
			}
		}
	}
}

func TestTeamCreateDerivesLeadThreadAndRegistersLeadAtomically(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	createdAt := time.Now().UTC()
	db.ExpectBegin()
	db.ExpectQuery("INSERT INTO agent_swarm_teams").
		WithArgs(pgxmock.AnyArg(), "thread-real", "review", "Review the release").
		WillReturnRows(pgxmock.NewRows([]string{"id", "lead_thread_id", "name", "description", "created_at"}).
			AddRow("team-1", "thread-real", "review", "Review the release", createdAt))
	db.ExpectExec("INSERT INTO agent_swarm_team_members").
		WithArgs("team-1", LeadAgentName, "thread-real", MemberStatusActive).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	db.ExpectCommit()

	runValues := map[string]any{}
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{RunID: "run-1", ThreadID: "thread-real", Values: runValues})
	result, err := findTool(t, manager, "team_create").Handler(ctx, tool.Call{Args: []byte(`{
		"name":"review",
		"description":"Review the release",
		"lead_thread_id":"thread-forged"
	}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("result = %#v", result)
	}
	var payload struct {
		Team Team   `json:"team"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Team.ID != "team-1" || payload.Team.LeadThreadID != "thread-real" || payload.Role != LeadAgentName {
		t.Fatalf("payload = %#v", payload)
	}
	if runValues["swarm_team_id"] != "team-1" || runValues["swarm_team_name"] != "review" {
		t.Fatalf("run values = %#v", runValues)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTeamCreateRollsBackWhenLeadRegistrationFails(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	db.ExpectBegin()
	db.ExpectQuery("INSERT INTO agent_swarm_teams").
		WithArgs("team-1", "thread-1", "review", nil).
		WillReturnRows(pgxmock.NewRows([]string{"id", "lead_thread_id", "name", "description", "created_at"}).
			AddRow("team-1", "thread-1", "review", "", time.Now().UTC()))
	db.ExpectExec("INSERT INTO agent_swarm_team_members").
		WithArgs("team-1", LeadAgentName, "thread-1", MemberStatusActive).
		WillReturnError(errors.New("member insert failed"))
	db.ExpectRollback()

	if _, err := manager.CreateTeam(context.Background(), "team-1", "review", "thread-1"); err == nil {
		t.Fatal("CreateTeam() succeeded after lead registration failed")
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTeamDeleteRejectsModelForgedLeadThread(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	db.ExpectQuery("SELECT id,lead_thread_id").WithArgs("thread-attacker", "team-victim").WillReturnRows(
		pgxmock.NewRows([]string{"id", "lead_thread_id", "name", "description", "created_at"}),
	)
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{RunID: "run-1", ThreadID: "thread-attacker"})
	result, err := findTool(t, manager, "team_delete").Handler(ctx, tool.Call{Args: []byte(`{
		"team_id":"team-victim",
		"lead_thread_id":"thread-victim"
	}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content, ErrTeamNotFound.Error()) {
		t.Fatalf("result = %#v", result)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTeamDeleteUsesTrustedLeadThread(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	createdAt := time.Now().UTC()
	db.ExpectQuery("SELECT id,lead_thread_id").WithArgs("thread-lead", "team-1").WillReturnRows(
		pgxmock.NewRows([]string{"id", "lead_thread_id", "name", "description", "created_at"}).
			AddRow("team-1", "thread-lead", "review", "", createdAt),
	)
	db.ExpectExec("DELETE FROM agent_swarm_teams").WithArgs("team-1", "thread-lead").WillReturnResult(pgxmock.NewResult("DELETE", 1))

	runValues := map[string]any{"swarm_team_id": "team-1", "swarm_team_name": "review"}
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{RunID: "run-1", ThreadID: "thread-lead", Values: runValues})
	result, err := findTool(t, manager, "team_delete").Handler(ctx, tool.Call{Args: []byte(`{"team_id":"team-1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !strings.Contains(result.Content, `"status":"deleted"`) {
		t.Fatalf("result = %#v", result)
	}
	if runValues["swarm_team_id"] != "" || runValues["swarm_team_name"] != "" {
		t.Fatalf("deleted team remained in run values: %#v", runValues)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMemberLifecycle(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	db.ExpectBegin()
	db.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs("team-1").WillReturnResult(pgxmock.NewResult("SELECT", 1))
	db.ExpectQuery("SELECT status FROM agent_swarm_team_members").WithArgs("team-1", "reviewer").WillReturnRows(
		pgxmock.NewRows([]string{"status"}),
	)
	db.ExpectQuery("SELECT COUNT").WithArgs("team-1").WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	db.ExpectExec("INSERT INTO agent_swarm_team_members").
		WithArgs("team-1", "reviewer", nil, "gpt-4o", "Review the patch", MemberStatusRunning).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	db.ExpectCommit()
	if err := manager.AddMember(context.Background(), Member{
		TeamID: "team-1", Name: "reviewer", Model: "gpt-4o", Prompt: "Review the patch", Status: MemberStatusRunning,
	}); err != nil {
		t.Fatal(err)
	}

	db.ExpectExec("UPDATE agent_swarm_team_members SET thread_id").
		WithArgs("team-1", "reviewer", "thread-reviewer", LeadAgentName).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := manager.BindMemberThread(context.Background(), "team-1", "reviewer", "thread-reviewer"); err != nil {
		t.Fatal(err)
	}

	db.ExpectExec("UPDATE agent_swarm_team_members SET status").
		WithArgs("team-1", "reviewer", MemberStatusRemoved, LeadAgentName).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := manager.RemoveMember(context.Background(), "team-1", "reviewer"); err != nil {
		t.Fatal(err)
	}

	if err := manager.UpdateMemberStatus(context.Background(), "team-1", "reviewer", "not-a-status"); err == nil {
		t.Fatal("UpdateMemberStatus() accepted an invalid lifecycle status")
	}
	if err := manager.AddMember(context.Background(), Member{TeamID: "team-1", Name: LeadAgentName}); err == nil {
		t.Fatal("AddMember() accepted the reserved lead identity")
	}
	if err := manager.AddMember(context.Background(), Member{TeamID: "team-1", Name: SystemAgentName}); err == nil {
		t.Fatal("AddMember() accepted the reserved lifecycle sender identity")
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterMemberCommitsMemberAndAnnouncementTogether(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	announcement := "[Joined] reviewer started working on: Review the patch"
	db.ExpectBegin()
	db.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs("team-1").
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	db.ExpectQuery("SELECT status FROM agent_swarm_team_members").
		WithArgs("team-1", "reviewer").
		WillReturnRows(pgxmock.NewRows([]string{"status"}))
	db.ExpectQuery("SELECT COUNT").
		WithArgs("team-1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	db.ExpectExec("INSERT INTO agent_swarm_team_members").
		WithArgs("team-1", "reviewer", nil, nil, "Review the patch", MemberStatusRunning).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	db.ExpectExec("INSERT INTO agent_swarm_messages").
		WithArgs("team-1", SystemAgentName, "*", announcement).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	db.ExpectCommit()

	if err := manager.RegisterMember(context.Background(), Member{
		TeamID: "team-1", Name: "reviewer", Prompt: "Review the patch", Status: MemberStatusRunning,
	}, announcement); err != nil {
		t.Fatal(err)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterMemberRollsBackWhenAnnouncementInsertFails(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	announcement := "[Joined] reviewer started working on: Review the patch"
	db.ExpectBegin()
	db.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs("team-1").
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	db.ExpectQuery("SELECT status FROM agent_swarm_team_members").
		WithArgs("team-1", "reviewer").
		WillReturnRows(pgxmock.NewRows([]string{"status"}))
	db.ExpectQuery("SELECT COUNT").
		WithArgs("team-1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	db.ExpectExec("INSERT INTO agent_swarm_team_members").
		WithArgs("team-1", "reviewer", nil, nil, "Review the patch", MemberStatusRunning).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	db.ExpectExec("INSERT INTO agent_swarm_messages").
		WithArgs("team-1", SystemAgentName, "*", announcement).
		WillReturnError(errors.New("announcement insert failed"))
	db.ExpectRollback()

	err = manager.RegisterMember(context.Background(), Member{
		TeamID: "team-1", Name: "reviewer", Prompt: "Review the patch", Status: MemberStatusRunning,
	}, announcement)
	if err == nil || !strings.Contains(err.Error(), "announcement insert failed") {
		t.Fatalf("RegisterMember() error = %v", err)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFinalizeMemberCommitsStatusAndAnnouncementTogether(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	announcement := "[Completed] reviewer: no issues found"
	db.ExpectBegin()
	db.ExpectExec("UPDATE agent_swarm_team_members SET status").
		WithArgs("team-1", "reviewer", MemberStatusCompleted, LeadAgentName).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	db.ExpectExec("INSERT INTO agent_swarm_messages").
		WithArgs("team-1", SystemAgentName, "*", announcement).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	db.ExpectCommit()

	if err := manager.FinalizeMember(
		context.Background(), "team-1", "reviewer", MemberStatusCompleted, announcement,
	); err != nil {
		t.Fatal(err)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFinalizeMemberRollsBackStatusWhenAnnouncementInsertFails(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	announcement := "[Failed] reviewer: model unavailable"
	db.ExpectBegin()
	db.ExpectExec("UPDATE agent_swarm_team_members SET status").
		WithArgs("team-1", "reviewer", MemberStatusFailed, LeadAgentName).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	db.ExpectExec("INSERT INTO agent_swarm_messages").
		WithArgs("team-1", SystemAgentName, "*", announcement).
		WillReturnError(errors.New("announcement insert failed"))
	db.ExpectRollback()

	err = manager.FinalizeMember(
		context.Background(), "team-1", "reviewer", MemberStatusFailed, announcement,
	)
	if err == nil || !strings.Contains(err.Error(), "announcement insert failed") {
		t.Fatalf("FinalizeMember() error = %v", err)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAtomicLifecycleAPIsRequireAnnouncementsAndTerminalStatus(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	if err := manager.RegisterMember(context.Background(), Member{TeamID: "team-1", Name: "reviewer"}, " "); err == nil {
		t.Fatal("RegisterMember() accepted an empty joined announcement")
	}
	if err := manager.FinalizeMember(context.Background(), "team-1", "reviewer", MemberStatusRunning, "still working"); err == nil {
		t.Fatal("FinalizeMember() accepted a non-terminal status")
	}
	if err := manager.FinalizeMember(context.Background(), "team-1", "reviewer", MemberStatusCompleted, " "); err == nil {
		t.Fatal("FinalizeMember() accepted an empty terminal announcement")
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAddMemberRejectsActiveNameWithoutOverwritingIdentity(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db, Options{MaxTeamSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	db.ExpectBegin()
	db.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs("team-1").WillReturnResult(pgxmock.NewResult("SELECT", 1))
	db.ExpectQuery("SELECT status FROM agent_swarm_team_members").WithArgs("team-1", "reviewer").WillReturnRows(
		pgxmock.NewRows([]string{"status"}).AddRow(MemberStatusRunning),
	)
	db.ExpectRollback()

	err = manager.AddMember(context.Background(), Member{TeamID: "team-1", Name: "reviewer", Status: MemberStatusRunning})
	if !errors.Is(err, ErrMemberActive) {
		t.Fatalf("error = %v, want ErrMemberActive", err)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAddMemberEnforcesMaxTeamSizeAtomically(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db, Options{MaxTeamSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	db.ExpectBegin()
	db.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs("team-1").WillReturnResult(pgxmock.NewResult("SELECT", 1))
	db.ExpectQuery("SELECT status FROM agent_swarm_team_members").WithArgs("team-1", "second-worker").WillReturnRows(
		pgxmock.NewRows([]string{"status"}),
	)
	db.ExpectQuery("SELECT COUNT").WithArgs("team-1").WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
	db.ExpectRollback()

	err = manager.AddMember(context.Background(), Member{TeamID: "team-1", Name: "second-worker", Status: MemberStatusRunning})
	if !errors.Is(err, ErrTeamFull) {
		t.Fatalf("error = %v, want ErrTeamFull", err)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAddMemberCanReuseTerminalNameAndClearsOldThread(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	db.ExpectBegin()
	db.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs("team-1").WillReturnResult(pgxmock.NewResult("SELECT", 1))
	db.ExpectQuery("SELECT status FROM agent_swarm_team_members").WithArgs("team-1", "reviewer").WillReturnRows(
		pgxmock.NewRows([]string{"status"}).AddRow(MemberStatusCompleted),
	)
	db.ExpectQuery("SELECT COUNT").WithArgs("team-1").WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	db.ExpectExec("INSERT INTO agent_swarm_team_members").
		WithArgs("team-1", "reviewer", nil, nil, "new task", MemberStatusRunning).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	db.ExpectCommit()

	if err := manager.AddMember(context.Background(), Member{TeamID: "team-1", Name: "reviewer", Prompt: "new task", Status: MemberStatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func findTool(t *testing.T, manager *Manager, name string) tool.Definition {
	t.Helper()
	for _, definition := range manager.Tools() {
		if definition.Name == name {
			return definition
		}
	}
	t.Fatalf("tool %q not found", name)
	return tool.Definition{}
}
