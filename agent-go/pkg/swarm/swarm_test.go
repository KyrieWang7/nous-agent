package swarm

import (
	"context"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
	"github.com/pashagolub/pgxmock/v4"
)

func TestBroadcastCreatesAReceiptPerMember(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	manager, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	for _, agent := range []string{"alice", "bob"} {
		db.ExpectBegin()
		db.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs("team-1", agent).WillReturnResult(pgxmock.NewResult("SELECT", 1))
		db.ExpectQuery("SELECT m.id").WithArgs("team-1", agent, 50).WillReturnRows(
			pgxmock.NewRows([]string{"id", "team_id", "from_agent", "to_agent", "content", "created_at"}).
				AddRow(int64(1), "team-1", "lead", "*", "hello", time.Now().UTC()),
		)
		db.ExpectExec("INSERT INTO agent_swarm_message_receipts").WithArgs([]int64{1}, agent).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		db.ExpectCommit()

		messages, err := manager.Poll(context.Background(), "team-1", agent, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) != 1 || messages[0].To != "*" {
			t.Fatalf("messages for %s = %#v", agent, messages)
		}
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

	db.ExpectQuery("SELECT team_id,agent_name").WithArgs("thread-alice", "team-1").WillReturnRows(
		pgxmock.NewRows([]string{"team_id", "agent_name"}).AddRow("team-1", "alice"),
	)
	db.ExpectQuery("SELECT EXISTS").WithArgs("team-1", "bob").WillReturnRows(
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
