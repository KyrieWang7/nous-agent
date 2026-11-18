package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	platformdb "github.com/KyrieWang7/nous-agent/platform-go/postgres"
)

func TestTeamsReadsDescription(t *testing.T) {
	connection, db, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	createdAt := time.Date(2026, time.August, 10, 8, 30, 0, 0, time.UTC)
	db.ExpectQuery("SELECT id,name,description,created_at FROM agent_swarm_teams").
		WithArgs("thread-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "description", "created_at"}).
			AddRow("team-1", "release", "Review the release", createdAt))

	database, err := platformdb.FromSQL(connection)
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{pool: database}
	teams, err := s.Teams(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(teams) != 1 || teams[0].Description == nil || *teams[0].Description != "Review the release" {
		t.Fatalf("teams=%+v", teams)
	}
	if !teams[0].CreatedAt.Equal(createdAt) {
		t.Fatalf("created_at=%v want %v", teams[0].CreatedAt, createdAt)
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTeamExistsDistinguishesExistingAndMissingTeams(t *testing.T) {
	connection, db, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	db.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM agent_swarm_teams WHERE id=\\$1\\)").
		WithArgs("team-1").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	db.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM agent_swarm_teams WHERE id=\\$1\\)").
		WithArgs("missing-team").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	database, err := platformdb.FromSQL(connection)
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{pool: database}
	exists, err := s.TeamExists(context.Background(), "team-1")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("existing team reported missing")
	}
	exists, err = s.TeamExists(context.Background(), "missing-team")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("missing team reported existing")
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMembersReadsModelAndJoinedAt(t *testing.T) {
	connection, db, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	joinedAt := time.Date(2026, time.August, 10, 8, 45, 0, 0, time.UTC)
	db.ExpectQuery("SELECT name,status,model,joined_at FROM agent_swarm_team_members").
		WithArgs("team-1").
		WillReturnRows(sqlmock.NewRows([]string{"name", "status", "model", "joined_at"}).
			AddRow("researcher", "running", "gpt-5", joinedAt).
			AddRow("reviewer", "active", nil, joinedAt.Add(time.Second)))

	database, err := platformdb.FromSQL(connection)
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{pool: database}
	members, err := s.Members(context.Background(), "team-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("members=%+v", members)
	}
	if members[0].Model == nil || *members[0].Model != "gpt-5" || members[0].JoinedAt == nil || !members[0].JoinedAt.Equal(joinedAt) {
		t.Fatalf("first member=%+v", members[0])
	}
	if members[1].Model != nil || members[1].JoinedAt == nil {
		t.Fatalf("second member=%+v", members[1])
	}
	if err := db.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
