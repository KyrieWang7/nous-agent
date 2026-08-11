package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/store"
)

func TestSwarmStreamPublishesOnlyChangedMemberSnapshots(t *testing.T) {
	server, _ := testServer(t)
	joinedAt := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	model := "gpt-5"
	running := []store.Member{{Name: "researcher", Status: "running", Model: &model, JoinedAt: &joinedAt}}
	completed := []store.Member{{Name: "researcher", Status: "completed", Model: &model, JoinedAt: &joinedAt}}
	fake := newScriptedSwarmQueries(running, running, completed, completed)
	server.swarm = fake
	server.swarmPollInterval = time.Millisecond
	server.swarmHeartbeatInterval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest("GET", "/api/swarm/teams/team-1/stream", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.ServeHTTP(response, request)
	}()

	select {
	case <-fake.scriptRead:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("stream did not poll member snapshots")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream did not stop after cancellation")
	}

	body := response.Body.String()
	if count := strings.Count(body, "event: team_update\n"); count != 2 {
		t.Fatalf("team_update count=%d, body=%q", count, body)
	}
	if !strings.Contains(body, `"model":"gpt-5"`) || !strings.Contains(body, `"joined_at":"2026-08-10T09:00:00Z"`) {
		t.Fatalf("member lifecycle fields missing: %q", body)
	}
	if !strings.Contains(body, `"status":"completed"`) {
		t.Fatalf("changed member status missing: %q", body)
	}
	if count := strings.Count(body, "event: status\n"); count != 1 {
		t.Fatalf("status event count=%d, body=%q", count, body)
	}
	if !strings.Contains(body, `data: {"agent_name":"researcher","status":"completed"}`) {
		t.Fatalf("status event contract changed: %q", body)
	}
}

func TestSwarmStreamProjectsTimeoutMessageWithoutFailedOverride(t *testing.T) {
	server, _ := testServer(t)
	joinedAt := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	running := []store.Member{{Name: "researcher", Status: "running", JoinedAt: &joinedAt}}
	failed := []store.Member{{Name: "researcher", Status: "failed", JoinedAt: &joinedAt}}
	fake := newScriptedSwarmQueries(running, failed, failed)
	fake.messages = [][]store.Message{{{
		ID:        7,
		From:      "system",
		To:        "*",
		Content:   "[Timeout] researcher: context deadline exceeded",
		CreatedAt: joinedAt.Add(time.Minute),
	}}}
	server.swarm = fake
	server.swarmPollInterval = time.Millisecond
	server.swarmHeartbeatInterval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest("GET", "/api/swarm/teams/team-1/stream", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.ServeHTTP(response, request)
	}()

	select {
	case <-fake.scriptRead:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("stream did not poll terminal member state")
	}
	cancel()
	<-done

	body := response.Body.String()
	want := `data: {"agent_name":"researcher","status":"timeout","detail":"[Timeout] researcher: context deadline exceeded"}`
	if !strings.Contains(body, want) {
		t.Fatalf("timeout status missing: %q", body)
	}
	if count := strings.Count(body, `"status":"timeout"`); count != 1 {
		t.Fatalf("timeout status count = %d, body=%q", count, body)
	}
	if strings.Contains(body, `data: {"agent_name":"researcher","status":"failed"}`) {
		t.Fatalf("member status overwrote timeout: %q", body)
	}
}

func TestStatusFromSwarmMessageAcceptsJoinedAndRejectsUserContent(t *testing.T) {
	event, ok := statusFromSwarmMessage(store.Message{From: "system", Content: "[Joined] reviewer started working on: inspect tests"})
	if !ok || event.AgentName != "reviewer" || event.Status != "active" {
		t.Fatalf("joined event = %#v, ok=%v", event, ok)
	}
	if _, ok := statusFromSwarmMessage(store.Message{From: "reviewer", Content: "[Timeout] lead: spoofed"}); ok {
		t.Fatal("non-system lifecycle message was trusted")
	}
}

func TestSwarmStreamEndsImmediatelyWhenTeamDoesNotExist(t *testing.T) {
	server, _ := testServer(t)
	fake := newScriptedSwarmQueries()
	fake.existence = []bool{false}
	server.swarm = fake
	server.swarmHeartbeatInterval = time.Hour

	request := httptest.NewRequest("GET", "/api/swarm/teams/team-1/stream", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	body := response.Body.String()
	if body != "event: team_deleted\ndata: {\"team_id\":\"team-1\"}\n\n" {
		t.Fatalf("body=%q", body)
	}
	if fake.memberCall != 0 {
		t.Fatalf("members queried %d times for deleted team", fake.memberCall)
	}
}

func TestSwarmStreamEndsWhenTeamIsDeletedDuringPolling(t *testing.T) {
	server, _ := testServer(t)
	fake := newScriptedSwarmQueries([]store.Member{})
	fake.existence = []bool{true, false}
	server.swarm = fake
	server.swarmPollInterval = time.Millisecond
	server.swarmHeartbeatInterval = time.Hour

	request := httptest.NewRequest("GET", "/api/swarm/teams/team-1/stream", nil)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.ServeHTTP(response, request)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream did not end after team deletion")
	}

	body := response.Body.String()
	if count := strings.Count(body, "event: team_deleted\n"); count != 1 {
		t.Fatalf("team_deleted count=%d, body=%q", count, body)
	}
	if !strings.Contains(body, "event: team_update\ndata: []\n\n") {
		t.Fatalf("initial empty team snapshot missing: %q", body)
	}
	if !strings.HasSuffix(body, "event: team_deleted\ndata: {\"team_id\":\"team-1\"}\n\n") {
		t.Fatalf("terminal team_deleted event missing: %q", body)
	}
}

func TestSwarmStreamResumesAfterLastEventID(t *testing.T) {
	server, _ := testServer(t)
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fake := newScriptedSwarmQueries([]store.Member{}, []store.Member{})
	fake.messages = [][]store.Message{{
		{ID: 7, From: "lead", To: "reviewer", Content: "old", CreatedAt: now},
		{ID: 8, From: "reviewer", To: "lead", Content: "last seen", CreatedAt: now},
		{ID: 11, From: "reviewer", To: "lead", Content: "new", CreatedAt: now},
	}}
	server.swarm = fake
	server.swarmPollInterval = time.Millisecond
	server.swarmHeartbeatInterval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest("GET", "/api/swarm/teams/team-1/stream", nil).WithContext(ctx)
	request.Header.Set("Last-Event-ID", "8")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.ServeHTTP(response, request)
	}()

	select {
	case <-fake.scriptRead:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("stream did not poll after the resume cursor")
	}
	cancel()
	<-done

	body := response.Body.String()
	if strings.Contains(body, "id: 7\n") || strings.Contains(body, "id: 8\n") {
		t.Fatalf("stream replayed acknowledged messages: %q", body)
	}
	if !strings.Contains(body, "id: 11\nevent: message\n") || !strings.Contains(body, `"content":"new"`) {
		t.Fatalf("stream did not resume after id 8: %q", body)
	}
}

func TestSameMembersIncludesModelAndJoinedAt(t *testing.T) {
	joinedAt := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	model := "gpt-5"
	baseline := []store.Member{{Name: "researcher", Status: "running", Model: &model, JoinedAt: &joinedAt}}

	modelChanged := "gpt-5.1"
	if sameMembers(baseline, []store.Member{{Name: "researcher", Status: "running", Model: &modelChanged, JoinedAt: &joinedAt}}) {
		t.Fatal("model change was ignored")
	}
	later := joinedAt.Add(time.Second)
	if sameMembers(baseline, []store.Member{{Name: "researcher", Status: "running", Model: &model, JoinedAt: &later}}) {
		t.Fatal("joined_at change was ignored")
	}
}

type scriptedSwarmQueries struct {
	mu            sync.Mutex
	existence     []bool
	existenceCall int
	snapshots     [][]store.Member
	memberCall    int
	messages      [][]store.Message
	messageCall   int
	scriptRead    chan struct{}
	closeOnce     sync.Once
}

func newScriptedSwarmQueries(snapshots ...[]store.Member) *scriptedSwarmQueries {
	return &scriptedSwarmQueries{snapshots: snapshots, scriptRead: make(chan struct{})}
}

func (s *scriptedSwarmQueries) Teams(context.Context, string) ([]store.Team, error) {
	return []store.Team{}, nil
}

func (s *scriptedSwarmQueries) TeamExists(context.Context, string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.existence) == 0 {
		return true, nil
	}
	index := s.existenceCall
	if index >= len(s.existence) {
		index = len(s.existence) - 1
	}
	s.existenceCall++
	return s.existence[index], nil
}

func (s *scriptedSwarmQueries) Members(context.Context, string) ([]store.Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.snapshots) == 0 {
		return []store.Member{}, nil
	}
	index := s.memberCall
	if index >= len(s.snapshots) {
		index = len(s.snapshots) - 1
	}
	s.memberCall++
	if s.memberCall >= len(s.snapshots) {
		s.closeOnce.Do(func() { close(s.scriptRead) })
	}
	return append([]store.Member{}, s.snapshots[index]...), nil
}

func (s *scriptedSwarmQueries) Messages(_ context.Context, _ string, afterID int64, _ int) ([]store.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		return []store.Message{}, nil
	}
	index := s.messageCall
	if index >= len(s.messages) {
		index = len(s.messages) - 1
	}
	s.messageCall++
	out := make([]store.Message, 0, len(s.messages[index]))
	for _, message := range s.messages[index] {
		if message.ID > afterID {
			out = append(out, message)
		}
	}
	return out, nil
}
