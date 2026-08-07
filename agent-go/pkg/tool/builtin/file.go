// Package builtin 提供内置工具。
//
// 全部文件与命令工具都经 sandbox.Handle 执行，不直接触碰宿主文件系统 ——
// 隔离边界只有一处，散落的 os.ReadFile 会让它形同不存在。
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// 工具组名，与 config.yaml 的 tool_groups 对齐。
const (
	GroupFileRead  = "file:read"
	GroupFileWrite = "file:write"
	GroupBash      = "bash"
)

// LS 返回列目录工具。
func LS() tool.Definition {
	return tool.Definition{
		Name:        "ls",
		Group:       GroupFileRead,
		Description: "List the entries of a directory inside the workspace.",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Directory path relative to the workspace root. Defaults to the root."}
  }
}`),
		Metadata: tool.Metadata{
			IsReadOnly:        true,
			IsConcurrencySafe: true,
			RequiresSandbox:   true,
		},
		Handler: handleLS,
	}
}

type lsArgs struct {
	Path string `json:"path"`
}

func handleLS(ctx context.Context, call tool.Call) (*tool.Result, error) {
	var args lsArgs
	if err := decode(call.Args, &args); err != nil {
		return errResult(err), nil
	}

	h, err := sandbox.HandleFromContext(ctx)
	if err != nil {
		return nil, err
	}

	entries, err := h.FS().List(ctx, args.Path)
	if err != nil {
		return errResult(err), nil
	}
	if len(entries) == 0 {
		return &tool.Result{Content: "(empty directory)"}, nil
	}

	var sb strings.Builder
	for _, e := range entries {
		if e.IsDir {
			sb.WriteString(e.Name + "/\n")
			continue
		}
		sb.WriteString(fmt.Sprintf("%s (%d bytes)\n", e.Name, e.Size))
	}
	return &tool.Result{Content: strings.TrimRight(sb.String(), "\n")}, nil
}

// ReadFile 返回读文件工具。
func ReadFile() tool.Definition {
	return tool.Definition{
		Name:        "read_file",
		Group:       GroupFileRead,
		Description: "Read a text file from the workspace. Returns numbered lines.",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path":   {"type": "string", "description": "File path relative to the workspace root."},
    "offset": {"type": "integer", "description": "1-based first line to return. Defaults to 1."},
    "limit":  {"type": "integer", "description": "Maximum number of lines to return."}
  },
  "required": ["path"]
}`),
		Metadata: tool.Metadata{
			IsReadOnly:        true,
			IsConcurrencySafe: true,
			RequiresSandbox:   true,
		},
		Handler: handleReadFile,
	}
}

type readFileArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

func handleReadFile(ctx context.Context, call tool.Call) (*tool.Result, error) {
	var args readFileArgs
	if err := decode(call.Args, &args); err != nil {
		return errResult(err), nil
	}
	if args.Path == "" {
		return errResult(fmt.Errorf("read_file: path is required")), nil
	}

	h, err := sandbox.HandleFromContext(ctx)
	if err != nil {
		return nil, err
	}

	data, err := h.FS().ReadFile(ctx, args.Path)
	if err != nil {
		return errResult(err), nil
	}

	lines := strings.Split(string(data), "\n")
	// 尾部换行会切出一个空元素，去掉它以免多报一行。
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}

	start := max(args.Offset-1, 0)
	if start >= len(lines) {
		return &tool.Result{Content: fmt.Sprintf("(offset %d is past the end of the file, which has %d lines)",
			args.Offset, len(lines))}, nil
	}
	end := len(lines)
	if args.Limit > 0 && start+args.Limit < end {
		end = start + args.Limit
	}

	var sb strings.Builder
	width := len(strconv.Itoa(end))
	for i := start; i < end; i++ {
		sb.WriteString(fmt.Sprintf("%*d\t%s\n", width, i+1, lines[i]))
	}
	if end < len(lines) {
		sb.WriteString(fmt.Sprintf("\n(showing lines %d-%d of %d)\n", start+1, end, len(lines)))
	}

	return &tool.Result{Content: strings.TrimRight(sb.String(), "\n")}, nil
}

// WriteFile 返回写文件工具。
func WriteFile() tool.Definition {
	return tool.Definition{
		Name:        "write_file",
		Group:       GroupFileWrite,
		Description: "Create or overwrite a file in the workspace.",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path":    {"type": "string", "description": "File path relative to the workspace root."},
    "content": {"type": "string", "description": "Full file content."}
  },
  "required": ["path", "content"]
}`),
		Metadata: tool.Metadata{RequiresSandbox: true},
		Handler:  handleWriteFile,
	}
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func handleWriteFile(ctx context.Context, call tool.Call) (*tool.Result, error) {
	var args writeFileArgs
	if err := decode(call.Args, &args); err != nil {
		return errResult(err), nil
	}
	if args.Path == "" {
		return errResult(fmt.Errorf("write_file: path is required")), nil
	}

	h, err := sandbox.HandleFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.FS().WriteFile(ctx, args.Path, []byte(args.Content)); err != nil {
		return errResult(err), nil
	}

	return &tool.Result{Content: fmt.Sprintf("Wrote %d bytes to %s", len(args.Content), args.Path)}, nil
}

// StrReplace 返回精确替换工具。
func StrReplace() tool.Definition {
	return tool.Definition{
		Name:  "str_replace",
		Group: GroupFileWrite,
		Description: "Replace an exact string in a file. The target string must occur exactly once; " +
			"include surrounding context to make it unique.",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path":    {"type": "string", "description": "File path relative to the workspace root."},
    "old_str": {"type": "string", "description": "Exact text to find, including whitespace and indentation."},
    "new_str": {"type": "string", "description": "Replacement text."}
  },
  "required": ["path", "old_str", "new_str"]
}`),
		Metadata: tool.Metadata{RequiresSandbox: true},
		Handler:  handleStrReplace,
	}
}

type strReplaceArgs struct {
	Path   string `json:"path"`
	OldStr string `json:"old_str"`
	NewStr string `json:"new_str"`
}

func handleStrReplace(ctx context.Context, call tool.Call) (*tool.Result, error) {
	var args strReplaceArgs
	if err := decode(call.Args, &args); err != nil {
		return errResult(err), nil
	}
	switch {
	case args.Path == "":
		return errResult(fmt.Errorf("str_replace: path is required")), nil
	case args.OldStr == "":
		return errResult(fmt.Errorf("str_replace: old_str must not be empty")), nil
	}

	h, err := sandbox.HandleFromContext(ctx)
	if err != nil {
		return nil, err
	}

	data, err := h.FS().ReadFile(ctx, args.Path)
	if err != nil {
		return errResult(err), nil
	}
	content := string(data)

	// 出现次数必须恰好为一：零次说明模型记错了内容，多次说明它没给足上下文。
	// 两种情况都不能猜，猜错就是静默改错地方。
	switch n := strings.Count(content, args.OldStr); n {
	case 1:
	case 0:
		return errResult(fmt.Errorf("str_replace: old_str not found in %s", args.Path)), nil
	default:
		return errResult(fmt.Errorf(
			"str_replace: old_str occurs %d times in %s; include more surrounding context to make it unique",
			n, args.Path)), nil
	}

	updated := strings.Replace(content, args.OldStr, args.NewStr, 1)
	if err := h.FS().WriteFile(ctx, args.Path, []byte(updated)); err != nil {
		return errResult(err), nil
	}
	return &tool.Result{Content: fmt.Sprintf("Replaced 1 occurrence in %s", args.Path)}, nil
}

func decode(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

// errResult 把可归因于模型的失败包成 error 结果而不是 Go error。
//
// 参数写错、文件不存在、替换目标不唯一，都是模型能自己纠正的问题：
// 回灌说明让它改换方案，比杀掉整个回合有用。
func errResult(err error) *tool.Result {
	return &tool.Result{Content: err.Error(), IsError: true}
}
