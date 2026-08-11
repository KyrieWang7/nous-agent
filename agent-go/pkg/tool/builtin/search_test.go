package builtin

import (
	"strings"
	"testing"
)

func TestSearchBudgetBoundsFilesAndBytes(t *testing.T) {
	t.Parallel()

	budget := searchBudget{maxFiles: 2, maxBytes: 10}
	if !budget.reserve(6) {
		t.Fatal("first reservation was rejected")
	}
	if budget.reserve(5) {
		t.Fatal("reservation beyond the byte budget succeeded")
	}
	if !budget.reserve(4) {
		t.Fatal("reservation that exactly fills the byte budget was rejected")
	}
	if !budget.exhausted() {
		t.Fatal("budget should be exhausted after reaching both limits")
	}
	if budget.reserve(0) {
		t.Fatal("reservation beyond the file budget succeeded")
	}
}

func TestAppendSearchNoticesMarksIncompleteNoMatch(t *testing.T) {
	t.Parallel()

	got := appendSearchNotices("(no matches)", true, false, 0)
	if !strings.Contains(got, "before all files were scanned") {
		t.Fatalf("incomplete no-match result = %q", got)
	}
}

func TestNormalizeSearchPatternRejectsNestedParentTraversal(t *testing.T) {
	t.Parallel()

	for _, pattern := range []string{"src/../../*.go", "src/../*.go"} {
		if _, err := normalizeSearchPattern(pattern); err == nil {
			t.Errorf("normalizeSearchPattern(%q) succeeded", pattern)
		}
	}
}

func TestMatchDoublestar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{pattern: "**/*.go", name: "main.go", want: true},
		{pattern: "**/*.go", name: "pkg/deep/file.go", want: true},
		{pattern: "pkg/**/file.go", name: "pkg/file.go", want: true},
		{pattern: "pkg/**/file.go", name: "pkg/deep/file.go", want: true},
		{pattern: "pkg/**/file.go", name: "other/file.go", want: false},
	}

	for _, tt := range tests {
		got, err := matchDoublestar(tt.pattern, tt.name)
		if err != nil || got != tt.want {
			t.Errorf("matchDoublestar(%q, %q) = %v, %v; want %v, nil", tt.pattern, tt.name, got, err, tt.want)
		}
	}
}
