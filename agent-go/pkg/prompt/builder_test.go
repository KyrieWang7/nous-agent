package prompt_test

import (
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/prompt"
)

func TestBuilder_SectionsAppearInDeclaredOrder(t *testing.T) {
	t.Parallel()

	got := prompt.New("BASE").
		Section("Skills", "skill body").
		Section("Subagents", "subagent body").
		Build()

	iBase := strings.Index(got, "BASE")
	iSkills := strings.Index(got, "skill body")
	iSub := strings.Index(got, "subagent body")

	if iBase < 0 || iSkills < 0 || iSub < 0 {
		t.Fatalf("built prompt is missing content:\n%s", got)
	}
	if iBase >= iSkills || iSkills >= iSub {
		t.Fatalf("sections out of order:\n%s", got)
	}
}

// 同一份输入两次装配必须逐字节一致：请求体稳定才能命中供应商侧的 prompt 缓存。
func TestBuilder_OutputIsByteStable(t *testing.T) {
	t.Parallel()

	build := func() string {
		return prompt.New("BASE").
			Section("Skills", "a").
			Section("Tools", "b").
			Build()
	}

	first, second := build(), build()
	if first != second {
		t.Fatal("Build() is not byte-stable across invocations")
	}
}

func TestBuilder_EmptySectionsAreOmitted(t *testing.T) {
	t.Parallel()

	got := prompt.New("BASE").
		Section("Skills", "").
		Section("Tools", "   ").
		Build()

	if strings.Contains(got, "Skills") || strings.Contains(got, "Tools") {
		t.Fatalf("empty sections must be omitted:\n%s", got)
	}
	if strings.TrimSpace(got) != "BASE" {
		t.Fatalf("Build() = %q, want just the base", got)
	}
}

func TestBuilder_SectionHeadingsAreRendered(t *testing.T) {
	t.Parallel()

	got := prompt.New("BASE").Section("Skills", "body").Build()

	if !strings.Contains(got, "## Skills") {
		t.Fatalf("section heading missing:\n%s", got)
	}
}

func TestBuilder_EmptyBaseIsAllowed(t *testing.T) {
	t.Parallel()

	got := prompt.New("").Section("Skills", "body").Build()

	if !strings.Contains(got, "body") {
		t.Fatalf("Build() dropped the section when base was empty:\n%s", got)
	}
	if strings.HasPrefix(got, "\n") {
		t.Fatalf("Build() left a leading blank line: %q", got)
	}
}

func TestBuilder_NoTrailingWhitespace(t *testing.T) {
	t.Parallel()

	got := prompt.New("BASE").Section("Skills", "body\n\n").Build()

	if got != strings.TrimRight(got, " \t\n") {
		t.Fatalf("Build() left trailing whitespace: %q", got)
	}
}

func TestBuilder_DuplicateSectionNamesBothRendered(t *testing.T) {
	t.Parallel()

	// 不去重：调用方若真要两段同名内容，那是它的选择，
	// 静默丢弃一段比渲染两段更难排查。
	got := prompt.New("BASE").
		Section("Notes", "one").
		Section("Notes", "two").
		Build()

	if strings.Count(got, "## Notes") != 2 {
		t.Fatalf("expected both sections rendered:\n%s", got)
	}
}
