package storage

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/pashagolub/pgxmock/v4"
)

func TestReplaceTranscriptSupersedesOldRowsAndWritesCompleteHistory(t *testing.T) {
	db, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	store, err := NewPostgres(db)
	if err != nil {
		t.Fatal(err)
	}

	transcript := []message.Message{
		{Role: message.RoleSystem, Content: "summary"},
		{Role: message.RoleUser, Content: "new question"},
	}
	db.ExpectBegin()
	db.ExpectQuery("SELECT id FROM agent_thread").WithArgs("thread-1").WillReturnRows(
		pgxmock.NewRows([]string{"id"}).AddRow("thread-1"),
	)
	db.ExpectQuery("SELECT COALESCE").WithArgs("thread-1").WillReturnRows(
		pgxmock.NewRows([]string{"seq"}).AddRow(int64(7)),
	)
	db.ExpectExec("UPDATE agent_message SET superseded=TRUE").WithArgs("thread-1").WillReturnResult(pgxmock.NewResult("UPDATE", 6))
	for i, msg := range transcript {
		raw, err := json.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		db.ExpectExec("INSERT INTO agent_message").WithArgs("thread-1", int64(7+i), msg.Role, raw).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	}
	db.ExpectCommit()
	db.ExpectRollback()

	if err := store.ReplaceTranscript(context.Background(), "thread-1", transcript); err != nil {
		t.Fatal(err)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
