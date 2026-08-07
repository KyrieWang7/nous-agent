package model_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

func TestUsage_Add(t *testing.T) {
	t.Parallel()

	a := model.Usage{InputTokens: 10, OutputTokens: 5, CachedInputTokens: 2}
	b := model.Usage{InputTokens: 3, OutputTokens: 7, CachedInputTokens: 1}

	got := a.Add(b)
	want := model.Usage{InputTokens: 13, OutputTokens: 12, CachedInputTokens: 3}

	if got != want {
		t.Fatalf("Add() = %+v, want %+v", got, want)
	}
	// a 不得被改写
	if a.InputTokens != 10 {
		t.Fatalf("Add mutated the receiver: %+v", a)
	}
}

func TestUsage_TotalTokens(t *testing.T) {
	t.Parallel()

	u := model.Usage{InputTokens: 10, OutputTokens: 5}
	if got, want := u.TotalTokens(), 15; got != want {
		t.Fatalf("TotalTokens() = %d, want %d", got, want)
	}
}

func TestErrorClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		overflow bool
		rate     bool
		unavail  bool
	}{
		{
			name:     "context overflow wrapped",
			err:      fmt.Errorf("provider said: %w", model.ErrContextOverflow),
			overflow: true,
		},
		{
			name: "rate limited wrapped twice",
			err:  fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", model.ErrRateLimited)),
			rate: true,
		},
		{
			name:    "provider unavailable",
			err:     fmt.Errorf("503: %w", model.ErrProviderUnavailable),
			unavail: true,
		},
		{
			name: "unrelated error matches nothing",
			err:  errors.New("boom"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := model.IsContextOverflow(tc.err); got != tc.overflow {
				t.Errorf("IsContextOverflow() = %v, want %v", got, tc.overflow)
			}
			if got := model.IsRateLimited(tc.err); got != tc.rate {
				t.Errorf("IsRateLimited() = %v, want %v", got, tc.rate)
			}
			if got := model.IsProviderUnavailable(tc.err); got != tc.unavail {
				t.Errorf("IsProviderUnavailable() = %v, want %v", got, tc.unavail)
			}
		})
	}
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	t.Parallel()

	r := model.NewRegistry()
	factory := func(model.ProviderConfig) (model.Model, error) { return nil, nil }

	if err := r.Register("oai", factory); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := r.Get("oai"); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
}

func TestRegistry_DuplicateRegisterFails(t *testing.T) {
	t.Parallel()

	r := model.NewRegistry()
	factory := func(model.ProviderConfig) (model.Model, error) { return nil, nil }

	if err := r.Register("oai", factory); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if err := r.Register("oai", factory); err == nil {
		t.Fatal("second Register() with the same name succeeded; duplicates must fail")
	}
}

func TestRegistry_RegisterRejectsEmptyNameAndNilFactory(t *testing.T) {
	t.Parallel()

	r := model.NewRegistry()

	if err := r.Register("", func(model.ProviderConfig) (model.Model, error) { return nil, nil }); err == nil {
		t.Error("Register() with empty name succeeded")
	}
	if err := r.Register("x", nil); err == nil {
		t.Error("Register() with nil factory succeeded")
	}
}

// 配置引用未注册的名字必须立刻失败并列出可用名字（设计文档 §16）。
// 静默跳过是 nous-agent 那两处死缝的成因。
func TestRegistry_UnknownNameFailsFastAndListsAvailable(t *testing.T) {
	t.Parallel()

	r := model.NewRegistry()
	mustRegister(t, r, "openai-compatible")
	mustRegister(t, r, "anthropic")

	_, err := r.Get("deepseek")
	if err == nil {
		t.Fatal("Get() with unknown provider succeeded; must fail fast")
	}
	msg := err.Error()
	for _, want := range []string{"deepseek", "anthropic", "openai-compatible"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q; the message must list available providers", msg, want)
		}
	}
}

func TestRegistry_NamesSorted(t *testing.T) {
	t.Parallel()

	r := model.NewRegistry()
	mustRegister(t, r, "zeta")
	mustRegister(t, r, "alpha")
	mustRegister(t, r, "mid")

	got := r.Names()
	want := []string{"alpha", "mid", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names() = %v, want %v (sorted)", got, want)
		}
	}
}

func TestRegistry_ConcurrentReads(t *testing.T) {
	t.Parallel()

	r := model.NewRegistry()
	mustRegister(t, r, "oai")

	done := make(chan struct{})
	for range 20 {
		go func() {
			defer func() { done <- struct{}{} }()
			_, _ = r.Get("oai")
			_ = r.Names()
		}()
	}
	for range 20 {
		<-done
	}
}

func mustRegister(t *testing.T, r *model.Registry, name string) {
	t.Helper()
	err := r.Register(name, func(model.ProviderConfig) (model.Model, error) { return nil, nil })
	if err != nil {
		t.Fatalf("Register(%q) error = %v", name, err)
	}
}
