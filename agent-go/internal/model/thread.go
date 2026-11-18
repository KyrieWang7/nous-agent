package model

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

type Thread struct {
	ThreadID string         `json:"thread_id"`
	Created  time.Time      `json:"created_at"`
	Updated  time.Time      `json:"updated_at"`
	Metadata map[string]any `json:"metadata"`
	Status   string         `json:"status"`
	Values   map[string]any `json:"values"`
}

type Run struct {
	RunID        string         `json:"run_id"`
	ThreadID     string         `json:"thread_id"`
	AssistantID  string         `json:"assistant_id"`
	Status       string         `json:"status"`
	Created      time.Time      `json:"created_at"`
	Completed    *time.Time     `json:"completed_at,omitempty"`
	Metadata     map[string]any `json:"metadata"`
	OnDisconnect string         `json:"on_disconnect"`
	RiskLevel    string         `json:"risk_level,omitempty"`
}

type RunUpdate struct {
	Status    string
	RiskLevel string
}

type RunCompletion struct {
	RunID             string
	ThreadID          string
	Status            string
	Iterations        int
	LLMCalls          int
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
	LeadTokens        int
	SubagentTokens    int
	AuxiliaryTokens   int
	CostMicros        int64
	Duration          time.Duration
	CompletedAt       time.Time
}

func NewThread(id string, metadata map[string]any) Thread {
	now := time.Now().UTC()
	return Thread{ThreadID: id, Created: now, Updated: now, Metadata: metadata, Status: "idle", Values: map[string]any{}}
}

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b[:])
	return strings.Join([]string{s[:8], s[8:12], s[12:16], s[16:20], s[20:]}, "-")
}

var (
	ErrThreadNotFound = errors.New("httpapi: thread not found")
	ErrRunNotFound    = errors.New("httpapi: run not found")
	ErrForkConflict   = errors.New("fork destination matches a different conversation")
)
