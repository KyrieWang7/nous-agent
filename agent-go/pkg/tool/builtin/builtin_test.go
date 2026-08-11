package builtin_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox/local"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool/builtin"
)

func newCtx(t *testing.T) (context.Context, sandbox.Handle) {
	t.Helper()

	p := local.NewProvider(local.Options{BaseDir: t.TempDir()})
	lease := sandbox.NewLease(p, "t1")
	ctx := sandbox.NewContext(context.Background(), lease)

	h, err := lease.Handle(ctx)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	return ctx, h
}

func run(t *testing.T, ctx context.Context, d tool.Definition, args string) *tool.Result {
	t.Helper()

	res, err := d.Handler(ctx, tool.Call{ID: "c1", Name: d.Name, Args: json.RawMessage(args)})
	if err != nil {
		t.Fatalf("%s handler error = %v", d.Name, err)
	}
	if res == nil {
		t.Fatalf("%s returned a nil result", d.Name)
	}
	return res
}

// --- 元数据（决定并发分段与沙箱要求）---

func TestMetadata_ReadOnlyToolsAreConcurrencySafe(t *testing.T) {
	t.Parallel()

	readers := []tool.Definition{builtin.LS(), builtin.Glob(), builtin.Grep(), builtin.ReadFile()}
	for _, d := range readers {
		if !d.Concurrent() {
			t.Errorf("%s should be read-only and concurrency-safe: %+v", d.Name, d.Metadata)
		}
	}

	writers := []tool.Definition{builtin.WriteFile(), builtin.StrReplace(), builtin.Bash()}
	for _, d := range writers {
		if d.Concurrent() {
			t.Errorf("%s must not be marked concurrent: %+v", d.Name, d.Metadata)
		}
	}
}

func TestViewImageReturnsMultimodalBlock(t *testing.T) {
	ctx, h := newCtx(t)
	if err := h.FS().WriteFile(ctx, "preview.png", []byte("png-data")); err != nil {
		t.Fatal(err)
	}
	res := run(t, ctx, builtin.ViewImage(), `{"image_path":"preview.png"}`)
	if res.IsError || len(res.ContentBlocks) != 1 {
		t.Fatalf("result = %#v", res)
	}
	if res.ContentBlocks[0].MimeType != "image/png" || res.ContentBlocks[0].Data == "" {
		t.Fatalf("block = %#v", res.ContentBlocks[0])
	}
}

func TestMetadata_AllRequireSandbox(t *testing.T) {
	t.Parallel()

	for _, d := range builtin.All() {
		if !d.Metadata.RequiresSandbox {
			t.Errorf("%s must declare RequiresSandbox", d.Name)
		}
	}
}

func TestAll_RegistersCleanly(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	if err := r.RegisterAll(builtin.All()...); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	if got := len(r.Names()); got != 9 {
		t.Fatalf("registered %d tools, want 9: %v", got, r.Names())
	}
}

func TestSchemas_AreValidJSON(t *testing.T) {
	t.Parallel()

	for _, d := range builtin.All() {
		var v map[string]any
		if err := json.Unmarshal(d.Parameters, &v); err != nil {
			t.Errorf("%s has invalid parameters schema: %v", d.Name, err)
		}
	}
}

// --- 没有沙箱时 ---

// 缺少沙箱是装配错误而不是模型错误：必须冒泡成 Go error，
// 让它在装配阶段就暴露，而不是变成一条模型看得懂的"失败"。
func TestTools_WithoutSandboxReturnFrameworkError(t *testing.T) {
	t.Parallel()

	for _, d := range builtin.All() {
		t.Run(d.Name, func(t *testing.T) {
			t.Parallel()
			_, err := d.Handler(context.Background(), tool.Call{Name: d.Name, Args: json.RawMessage(`{"path":"x","command":"ls","content":"","old_str":"a","new_str":"b"}`)})
			if err == nil {
				t.Fatalf("%s succeeded without a sandbox in context", d.Name)
			}
		})
	}
}

// --- ls ---

func TestLS(t *testing.T) {
	t.Parallel()

	ctx, h := newCtx(t)
	if err := h.FS().WriteFile(ctx, "dir/a.txt", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := h.FS().WriteFile(ctx, "dir/sub/b.txt", []byte("x")); err != nil {
		t.Fatal(err)
	}

	res := run(t, ctx, builtin.LS(), `{"path":"dir"}`)
	if res.IsError {
		t.Fatalf("ls failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "a.txt") || !strings.Contains(res.Content, "sub/") {
		t.Fatalf("ls output = %q", res.Content)
	}
}

func TestLS_EmptyDirectory(t *testing.T) {
	t.Parallel()

	ctx, _ := newCtx(t)
	res := run(t, ctx, builtin.LS(), `{}`)
	if !strings.Contains(res.Content, "empty") {
		t.Fatalf("ls on an empty root = %q", res.Content)
	}
}

func TestLS_EscapeBecomesErrorResult(t *testing.T) {
	t.Parallel()

	ctx, _ := newCtx(t)
	res := run(t, ctx, builtin.LS(), `{"path":"../../etc"}`)
	if !res.IsError {
		t.Fatalf("ls outside the sandbox should be an error result, got %q", res.Content)
	}
}

// --- glob / grep ---

func TestGlob_DoublestarFindsNestedFiles(t *testing.T) {
	t.Parallel()

	ctx, h := newCtx(t)
	for name, content := range map[string]string{
		"src/main.go":        "package main\n",
		"src/deep/helper.go": "package helper\n",
		"src/deep/readme.md": "docs\n",
	} {
		if err := h.FS().WriteFile(ctx, name, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}

	res := run(t, ctx, builtin.Glob(), `{"pattern":"**/*.go","path":"src"}`)
	if res.IsError {
		t.Fatalf("glob failed: %s", res.Content)
	}
	for _, want := range []string{"src/main.go", "src/deep/helper.go"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("glob output missing %q:\n%s", want, res.Content)
		}
	}
	if strings.Contains(res.Content, "readme.md") {
		t.Fatalf("glob included a non-matching file: %s", res.Content)
	}
}

func TestGlob_RejectsEscapingSearchRoot(t *testing.T) {
	t.Parallel()

	ctx, _ := newCtx(t)
	res := run(t, ctx, builtin.Glob(), `{"pattern":"**/*","path":"../outside"}`)
	if !res.IsError || !strings.Contains(res.Content, "escapes") {
		t.Fatalf("glob escape result = %#v", res)
	}
}

func TestGrep_SearchesRegexAndFiltersFiles(t *testing.T) {
	t.Parallel()

	ctx, h := newCtx(t)
	for name, content := range map[string]string{
		"pkg/a.go": "package a\nfunc Target() {}\n",
		"pkg/b.go": "package b\nfunc Other() {}\n",
		"pkg/a.md": "Target should not be returned\n",
	} {
		if err := h.FS().WriteFile(ctx, name, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}

	res := run(t, ctx, builtin.Grep(), `{"pattern":"func\\s+Target","path":"pkg","glob":"**/*.go"}`)
	if res.IsError {
		t.Fatalf("grep failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "pkg/a.go:2:func Target() {}") {
		t.Fatalf("grep output = %q", res.Content)
	}
	if strings.Contains(res.Content, "a.md") || strings.Contains(res.Content, "b.go") {
		t.Fatalf("grep returned a non-match: %s", res.Content)
	}
}

func TestGrep_InvalidRegexIsModelError(t *testing.T) {
	t.Parallel()

	ctx, _ := newCtx(t)
	res := run(t, ctx, builtin.Grep(), `{"pattern":"["}`)
	if !res.IsError || !strings.Contains(res.Content, "invalid pattern") {
		t.Fatalf("grep invalid-regex result = %#v", res)
	}
}

func TestGrep_SingleFileGlobUsesBaseName(t *testing.T) {
	t.Parallel()

	ctx, h := newCtx(t)
	if err := h.FS().WriteFile(ctx, "pkg/a.go", []byte("const Needle = true\n")); err != nil {
		t.Fatal(err)
	}
	res := run(t, ctx, builtin.Grep(), `{"pattern":"Needle","path":"pkg/a.go","glob":"*.go"}`)
	if res.IsError || !strings.Contains(res.Content, "pkg/a.go:1:") {
		t.Fatalf("grep single-file result = %#v", res)
	}
}

func TestGrep_NormalizesBackslashesInSearchPath(t *testing.T) {
	t.Parallel()

	ctx, h := newCtx(t)
	if err := h.FS().WriteFile(ctx, "pkg/a.go", []byte("const Needle = true\n")); err != nil {
		t.Fatal(err)
	}
	res := run(t, ctx, builtin.Grep(), `{"pattern":"Needle","path":"pkg\\a.go"}`)
	if res.IsError || !strings.Contains(res.Content, "pkg/a.go:1:") {
		t.Fatalf("grep normalized-path result = %#v", res)
	}
}

func TestGrep_TruncatesAfterResultLimit(t *testing.T) {
	t.Parallel()

	ctx, h := newCtx(t)
	var content strings.Builder
	for i := 0; i <= 200; i++ {
		content.WriteString("Needle\n")
	}
	if err := h.FS().WriteFile(ctx, "many.txt", []byte(content.String())); err != nil {
		t.Fatal(err)
	}

	res := run(t, ctx, builtin.Grep(), `{"pattern":"Needle","path":"many.txt"}`)
	if res.IsError || !strings.Contains(res.Content, "results truncated after 200 matches") {
		t.Fatalf("grep truncation result = %#v", res)
	}
	matchBlock := strings.SplitN(res.Content, "\n\n", 2)[0]
	if got := len(strings.Split(matchBlock, "\n")); got != 200 {
		t.Fatalf("grep returned %d matches, want 200", got)
	}
}

func TestGrep_DoesNotInventLineAfterTrailingNewline(t *testing.T) {
	t.Parallel()

	ctx, h := newCtx(t)
	if err := h.FS().WriteFile(ctx, "line.txt", []byte("content\n")); err != nil {
		t.Fatal(err)
	}

	res := run(t, ctx, builtin.Grep(), `{"pattern":"^$","path":"line.txt"}`)
	if res.IsError || res.Content != "(no matches)" {
		t.Fatalf("grep trailing-newline result = %#v", res)
	}
}

// --- read_file ---

func TestReadFile_NumbersLines(t *testing.T) {
	t.Parallel()

	ctx, h := newCtx(t)
	if err := h.FS().WriteFile(ctx, "a.txt", []byte("one\ntwo\nthree\n")); err != nil {
		t.Fatal(err)
	}

	res := run(t, ctx, builtin.ReadFile(), `{"path":"a.txt"}`)
	if res.IsError {
		t.Fatalf("read_file failed: %s", res.Content)
	}
	for _, want := range []string{"1\tone", "2\ttwo", "3\tthree"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("read_file output missing %q:\n%s", want, res.Content)
		}
	}
}

// 尾部换行不得多报一行：模型会据此认为文件末尾有个空行。
func TestReadFile_TrailingNewlineDoesNotAddALine(t *testing.T) {
	t.Parallel()

	ctx, h := newCtx(t)
	if err := h.FS().WriteFile(ctx, "a.txt", []byte("one\ntwo\n")); err != nil {
		t.Fatal(err)
	}

	res := run(t, ctx, builtin.ReadFile(), `{"path":"a.txt"}`)
	if strings.Contains(res.Content, "3\t") {
		t.Fatalf("read_file reported a phantom third line:\n%s", res.Content)
	}
}

func TestReadFile_OffsetAndLimit(t *testing.T) {
	t.Parallel()

	ctx, h := newCtx(t)
	if err := h.FS().WriteFile(ctx, "a.txt", []byte("l1\nl2\nl3\nl4\nl5\n")); err != nil {
		t.Fatal(err)
	}

	res := run(t, ctx, builtin.ReadFile(), `{"path":"a.txt","offset":2,"limit":2}`)
	if strings.Contains(res.Content, "l1") || strings.Contains(res.Content, "l4") {
		t.Fatalf("offset/limit not respected:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "l2") || !strings.Contains(res.Content, "l3") {
		t.Fatalf("offset/limit dropped requested lines:\n%s", res.Content)
	}
	// 截断时必须说明，否则模型以为读完了
	if !strings.Contains(res.Content, "of 5") {
		t.Fatalf("truncated read must report the total line count:\n%s", res.Content)
	}
}

func TestReadFile_OffsetPastEnd(t *testing.T) {
	t.Parallel()

	ctx, h := newCtx(t)
	if err := h.FS().WriteFile(ctx, "a.txt", []byte("only\n")); err != nil {
		t.Fatal(err)
	}

	res := run(t, ctx, builtin.ReadFile(), `{"path":"a.txt","offset":99}`)
	if !strings.Contains(res.Content, "past the end") {
		t.Fatalf("read_file past EOF = %q", res.Content)
	}
}

func TestReadFile_MissingPathIsErrorResult(t *testing.T) {
	t.Parallel()

	ctx, _ := newCtx(t)
	res := run(t, ctx, builtin.ReadFile(), `{}`)
	if !res.IsError {
		t.Fatalf("read_file without a path should be an error result, got %q", res.Content)
	}
}

func TestReadFile_InvalidJSONIsErrorResult(t *testing.T) {
	t.Parallel()

	ctx, _ := newCtx(t)
	res := run(t, ctx, builtin.ReadFile(), `{"path": 42}`)
	if !res.IsError {
		t.Fatalf("read_file with a bad argument type should be an error result, got %q", res.Content)
	}
}

// --- write_file ---

func TestWriteFile_CreatesParentDirectories(t *testing.T) {
	t.Parallel()

	ctx, h := newCtx(t)
	res := run(t, ctx, builtin.WriteFile(), `{"path":"deep/nested/a.txt","content":"hi"}`)
	if res.IsError {
		t.Fatalf("write_file failed: %s", res.Content)
	}

	got, err := h.FS().ReadFile(ctx, "deep/nested/a.txt")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != "hi" {
		t.Fatalf("file content = %q", got)
	}
}

func TestWriteFile_Overwrites(t *testing.T) {
	t.Parallel()

	ctx, h := newCtx(t)
	run(t, ctx, builtin.WriteFile(), `{"path":"a.txt","content":"first"}`)
	run(t, ctx, builtin.WriteFile(), `{"path":"a.txt","content":"second"}`)

	got, _ := h.FS().ReadFile(ctx, "a.txt")
	if string(got) != "second" {
		t.Fatalf("file content = %q, want %q", got, "second")
	}
}

func TestWriteFile_EscapeBecomesErrorResult(t *testing.T) {
	t.Parallel()

	ctx, _ := newCtx(t)
	res := run(t, ctx, builtin.WriteFile(), `{"path":"../escaped.txt","content":"x"}`)
	if !res.IsError {
		t.Fatal("write_file outside the sandbox should be an error result")
	}
}

// --- str_replace ---

func TestStrReplace_ReplacesUniqueOccurrence(t *testing.T) {
	t.Parallel()

	ctx, h := newCtx(t)
	if err := h.FS().WriteFile(ctx, "a.go", []byte("func old() {}\n")); err != nil {
		t.Fatal(err)
	}

	res := run(t, ctx, builtin.StrReplace(), `{"path":"a.go","old_str":"func old()","new_str":"func renamed()"}`)
	if res.IsError {
		t.Fatalf("str_replace failed: %s", res.Content)
	}

	got, _ := h.FS().ReadFile(ctx, "a.go")
	if string(got) != "func renamed() {}\n" {
		t.Fatalf("file content = %q", got)
	}
}

// 出现次数不唯一时必须拒绝：猜哪一处就是静默改错地方。
func TestStrReplace_RejectsAmbiguousTarget(t *testing.T) {
	t.Parallel()

	ctx, h := newCtx(t)
	if err := h.FS().WriteFile(ctx, "a.txt", []byte("x = 1\nx = 1\n")); err != nil {
		t.Fatal(err)
	}

	res := run(t, ctx, builtin.StrReplace(), `{"path":"a.txt","old_str":"x = 1","new_str":"x = 2"}`)
	if !res.IsError {
		t.Fatal("str_replace with an ambiguous target should be an error result")
	}
	if !strings.Contains(res.Content, "2 times") {
		t.Errorf("error should report the occurrence count: %q", res.Content)
	}

	// 文件必须保持不变
	got, _ := h.FS().ReadFile(ctx, "a.txt")
	if string(got) != "x = 1\nx = 1\n" {
		t.Fatalf("file was modified despite the ambiguity: %q", got)
	}
}

func TestStrReplace_RejectsMissingTarget(t *testing.T) {
	t.Parallel()

	ctx, h := newCtx(t)
	if err := h.FS().WriteFile(ctx, "a.txt", []byte("hello\n")); err != nil {
		t.Fatal(err)
	}

	res := run(t, ctx, builtin.StrReplace(), `{"path":"a.txt","old_str":"goodbye","new_str":"x"}`)
	if !res.IsError || !strings.Contains(res.Content, "not found") {
		t.Fatalf("str_replace result = %+v, want a not-found error", res)
	}
}

func TestStrReplace_RejectsEmptyOldStr(t *testing.T) {
	t.Parallel()

	ctx, h := newCtx(t)
	if err := h.FS().WriteFile(ctx, "a.txt", []byte("hello\n")); err != nil {
		t.Fatal(err)
	}

	// 空串在每个位置都"匹配"，替换语义无意义
	res := run(t, ctx, builtin.StrReplace(), `{"path":"a.txt","old_str":"","new_str":"x"}`)
	if !res.IsError {
		t.Fatal("str_replace with an empty old_str should be an error result")
	}
}

// --- bash ---

func TestBash_ReportsStdoutAndExitCode(t *testing.T) {
	t.Parallel()

	ctx, _ := newCtx(t)
	res := run(t, ctx, builtin.Bash(), `{"command":"echo hi"}`)
	if res.IsError {
		t.Fatalf("bash failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "hi") || !strings.Contains(res.Content, "Exit code: 0") {
		t.Fatalf("bash output = %q", res.Content)
	}
}

// 退出码与 stderr 必须始终可见：只回 stdout 会让模型把失败当成功。
func TestBash_NonZeroExitIsMarkedAsError(t *testing.T) {
	t.Parallel()

	ctx, _ := newCtx(t)
	res := run(t, ctx, builtin.Bash(), `{"command":"echo oops >&2; exit 7"}`)

	if !res.IsError {
		t.Fatal("a non-zero exit must be surfaced as an error result")
	}
	if !strings.Contains(res.Content, "Exit code: 7") {
		t.Errorf("bash output missing the exit code: %q", res.Content)
	}
	if !strings.Contains(res.Content, "oops") {
		t.Errorf("bash output missing stderr: %q", res.Content)
	}
}

func TestBash_TimeoutIsMarkedAsError(t *testing.T) {
	t.Parallel()

	ctx, _ := newCtx(t)
	res := run(t, ctx, builtin.Bash(), `{"command":"sleep 5","timeout_sec":1}`)

	if !res.IsError || !strings.Contains(res.Content, "timed out") {
		t.Fatalf("bash timeout result = %+v", res)
	}
}

func TestBash_RunsInWorkdir(t *testing.T) {
	t.Parallel()

	ctx, h := newCtx(t)
	if err := h.FS().WriteFile(ctx, "sub/marker.txt", []byte("x")); err != nil {
		t.Fatal(err)
	}

	res := run(t, ctx, builtin.Bash(), `{"command":"ls","workdir":"sub"}`)
	if !strings.Contains(res.Content, "marker.txt") {
		t.Fatalf("bash did not run in the requested workdir: %q", res.Content)
	}
}

func TestBash_WorkdirEscapeBecomesErrorResult(t *testing.T) {
	t.Parallel()

	ctx, _ := newCtx(t)
	res := run(t, ctx, builtin.Bash(), `{"command":"ls","workdir":"../.."}`)
	if !res.IsError {
		t.Fatal("bash with an escaping workdir should be an error result")
	}
}

func TestBash_EmptyCommandIsErrorResult(t *testing.T) {
	t.Parallel()

	ctx, _ := newCtx(t)
	res := run(t, ctx, builtin.Bash(), `{"command":"   "}`)
	if !res.IsError {
		t.Fatal("bash with a blank command should be an error result")
	}
}

func TestBash_NoOutputIsStated(t *testing.T) {
	t.Parallel()

	ctx, _ := newCtx(t)
	res := run(t, ctx, builtin.Bash(), `{"command":"true"}`)
	if !strings.Contains(res.Content, "no output") {
		t.Fatalf("bash with no output = %q; silence must be explicit", res.Content)
	}
}
