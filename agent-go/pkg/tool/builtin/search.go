package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

const (
	maxSearchEntries  = 100_000
	maxSearchResults  = 200
	maxGrepFiles      = 10_000
	maxGrepBytes      = int64(64 << 20)
	maxMatchLineRunes = 500
)

// Glob finds workspace files with doublestar-aware path patterns. Traversal
// goes through sandbox.FS so local and container-backed runs share the same
// path-escape and symlink policy.
func Glob() tool.Definition {
	return tool.Definition{
		Name:        "glob",
		Group:       GroupFileRead,
		Description: "Find files in the workspace by a glob pattern. Supports ** for recursive directory matching.",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "pattern": {"type": "string", "description": "Relative glob pattern, for example src/**/*.go."},
    "path": {"type": "string", "description": "Directory to search from. Defaults to the workspace root."}
  },
  "required": ["pattern"]
}`),
		Metadata: tool.Metadata{IsReadOnly: true, IsConcurrencySafe: true, RequiresSandbox: true},
		Handler:  handleGlob,
	}
}

type globArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

func handleGlob(ctx context.Context, call tool.Call) (*tool.Result, error) {
	var args globArgs
	if err := decode(call.Args, &args); err != nil {
		return errResult(err), nil
	}
	h, err := sandbox.HandleFromContext(ctx)
	if err != nil {
		return nil, err
	}
	pattern, err := normalizeSearchPattern(args.Pattern)
	if err != nil {
		return errResult(fmt.Errorf("glob: %w", err)), nil
	}

	files, scanTruncated, err := walkFiles(ctx, h.FS(), args.Path, maxSearchEntries)
	if err != nil {
		if isContextError(err) {
			return nil, err
		}
		return errResult(err), nil
	}
	matches := make([]string, 0)
	resultTruncated := false
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rel := relativeSearchPath(args.Path, file)
		matched, matchErr := matchDoublestar(pattern, rel)
		if matchErr != nil {
			return errResult(fmt.Errorf("glob: invalid pattern: %w", matchErr)), nil
		}
		if !matched {
			continue
		}
		matches = append(matches, file)
		if len(matches) > maxSearchResults {
			matches = matches[:maxSearchResults]
			resultTruncated = true
			break
		}
	}
	content := "(no matching files)"
	sort.Strings(matches)
	if len(matches) > 0 {
		content = strings.Join(matches, "\n")
	}
	content = appendSearchNotices(content, scanTruncated, resultTruncated, 0)
	return &tool.Result{Content: content}, nil
}

// Grep searches text files with Go's RE2 regular-expression syntax.
func Grep() tool.Definition {
	return tool.Definition{
		Name:        "grep",
		Group:       GroupFileRead,
		Description: "Search text files in the workspace with a regular expression. Returns path, line number, and matching line.",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "pattern": {"type": "string", "description": "RE2 regular expression to search for."},
    "path": {"type": "string", "description": "File or directory to search. Defaults to the workspace root."},
    "glob": {"type": "string", "description": "Optional file glob filter, for example **/*.go."}
  },
  "required": ["pattern"]
}`),
		Metadata: tool.Metadata{IsReadOnly: true, IsConcurrencySafe: true, RequiresSandbox: true},
		Handler:  handleGrep,
	}
}

type grepArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Glob    string `json:"glob"`
}

func handleGrep(ctx context.Context, call tool.Call) (*tool.Result, error) {
	var args grepArgs
	if err := decode(call.Args, &args); err != nil {
		return errResult(err), nil
	}
	h, err := sandbox.HandleFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return errResult(errors.New("grep: pattern is required")), nil
	}
	if len(args.Pattern) > 4096 {
		return errResult(errors.New("grep: pattern is too long")), nil
	}
	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return errResult(fmt.Errorf("grep: invalid pattern: %w", err)), nil
	}
	filter := ""
	if strings.TrimSpace(args.Glob) != "" {
		filter, err = normalizeSearchPattern(args.Glob)
		if err != nil {
			return errResult(fmt.Errorf("grep: invalid glob: %w", err)), nil
		}
	}

	files, scanTruncated, err := searchTargets(ctx, h.FS(), args.Path)
	if err != nil {
		if isContextError(err) {
			return nil, err
		}
		return errResult(err), nil
	}

	matches := make([]string, 0)
	skipped := 0
	resultTruncated := false
	budget := searchBudget{maxFiles: maxGrepFiles, maxBytes: maxGrepBytes}

searchLoop:
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if filter != "" {
			matched, matchErr := matchDoublestar(filter, relativeSearchPath(args.Path, file))
			if matchErr != nil {
				return errResult(fmt.Errorf("grep: invalid glob: %w", matchErr)), nil
			}
			if !matched {
				continue
			}
		}
		if budget.exhausted() {
			scanTruncated = true
			break
		}
		info, statErr := h.FS().Stat(ctx, file)
		if statErr != nil {
			if isContextError(statErr) {
				return nil, statErr
			}
			skipped++
			continue
		}
		if info.IsDir || info.Size < 0 || info.Size > sandbox.MaxReadFileBytes {
			skipped++
			continue
		}
		if !budget.reserve(info.Size) {
			scanTruncated = true
			continue
		}
		data, readErr := h.FS().ReadFile(ctx, file)
		if readErr != nil {
			if isContextError(readErr) {
				return nil, readErr
			}
			// One unreadable or oversized artifact must not make a repository-wide
			// search useless. The result reports the omission explicitly.
			skipped++
			continue
		}
		if bytes.IndexByte(data, 0) >= 0 {
			skipped++
			continue
		}
		lineStart := 0
		lineNumber := 1
		for lineStart < len(data) {
			lineEnd := bytes.IndexByte(data[lineStart:], '\n')
			if lineEnd < 0 {
				lineEnd = len(data)
			} else {
				lineEnd += lineStart
			}
			line := data[lineStart:lineEnd]
			if re.Match(line) {
				matches = append(matches, fmt.Sprintf("%s:%d:%s", file, lineNumber, truncateMatchLine(string(line))))
				if len(matches) > maxSearchResults {
					matches = matches[:maxSearchResults]
					resultTruncated = true
					break searchLoop
				}
			}
			if lineEnd == len(data) {
				break
			}
			lineStart = lineEnd + 1
			lineNumber++
			if lineStart == len(data) {
				break
			}
		}
	}
	content := "(no matches)"
	if len(matches) > 0 {
		content = strings.Join(matches, "\n")
	}
	content = appendSearchNotices(content, scanTruncated, resultTruncated, skipped)
	return &tool.Result{Content: content}, nil
}

type searchBudget struct {
	maxFiles int
	maxBytes int64
	files    int
	bytes    int64
}

func (b *searchBudget) reserve(size int64) bool {
	if size < 0 || b.files >= b.maxFiles || size > b.maxBytes-b.bytes {
		return false
	}
	b.files++
	b.bytes += size
	return true
}

func (b *searchBudget) exhausted() bool {
	return b.files >= b.maxFiles || b.bytes >= b.maxBytes
}

func appendSearchNotices(content string, scanTruncated, resultTruncated bool, skipped int) string {
	notices := make([]string, 0, 3)
	if resultTruncated {
		notices = append(notices, fmt.Sprintf("results truncated after %d matches", maxSearchResults))
	}
	if scanTruncated {
		notices = append(notices, "search stopped before all files were scanned")
	}
	if skipped > 0 {
		notices = append(notices, fmt.Sprintf("%d unreadable, oversized, or binary files skipped", skipped))
	}
	if len(notices) == 0 {
		return content
	}
	return content + "\n\n(" + strings.Join(notices, "; ") + ")"
}

func searchTargets(ctx context.Context, fsys sandbox.FS, root string) ([]string, bool, error) {
	root = cleanSearchPath(root)
	info, err := fsys.Stat(ctx, root)
	if err != nil {
		return nil, false, err
	}
	if !info.IsDir {
		return []string{cleanSearchPath(root)}, false, nil
	}
	return walkFiles(ctx, fsys, root, maxSearchEntries)
}

func walkFiles(ctx context.Context, fsys sandbox.FS, root string, limit int) ([]string, bool, error) {
	root = cleanSearchPath(root)
	queue := []string{root}
	files := make([]string, 0)
	visited := 0
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		dir := queue[0]
		queue = queue[1:]
		remaining := limit - visited
		if remaining <= 0 {
			sort.Strings(files)
			return files, true, nil
		}
		entries, dirTruncated, err := sandbox.ListWithLimit(ctx, fsys, dir, remaining)
		if err != nil {
			return nil, false, err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			visited++
			name := path.Join(dir, entry.Name)
			if entry.IsDir {
				queue = append(queue, name)
				continue
			}
			files = append(files, name)
		}
		if dirTruncated {
			sort.Strings(files)
			return files, true, nil
		}
	}
	sort.Strings(files)
	return files, false, nil
}

func cleanSearchPath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || value == "." {
		return "."
	}
	return path.Clean(value)
}

func relativeSearchPath(root, file string) string {
	root = cleanSearchPath(root)
	file = cleanSearchPath(file)
	if root == "." {
		return strings.TrimPrefix(file, "./")
	}
	if file == root {
		return path.Base(file)
	}
	if strings.HasPrefix(file, root+"/") {
		return strings.TrimPrefix(file, root+"/")
	}
	return file
}

func normalizeSearchPattern(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return "", errors.New("pattern is required")
	}
	if len(value) > 4096 {
		return "", errors.New("pattern is too long")
	}
	if strings.HasPrefix(value, "/") || value == ".." || strings.HasPrefix(value, "../") {
		return "", errors.New("pattern must be relative to the search path")
	}
	for _, part := range splitSearchPath(value) {
		if part == ".." {
			return "", errors.New("pattern must not traverse above the search path")
		}
	}
	value = strings.TrimPrefix(path.Clean(value), "./")
	parts := splitSearchPath(value)
	if len(parts) > 256 {
		return "", errors.New("pattern has too many path segments")
	}
	for _, part := range parts {
		if part == "**" {
			continue
		}
		if _, err := path.Match(part, ""); err != nil {
			return "", err
		}
	}
	return value, nil
}

func matchDoublestar(pattern, name string) (bool, error) {
	patternParts := splitSearchPath(pattern)
	nameParts := splitSearchPath(name)
	for _, part := range patternParts {
		if part == "**" {
			continue
		}
		if _, err := path.Match(part, ""); err != nil {
			return false, err
		}
	}

	matched := make([]bool, len(nameParts)+1)
	matched[0] = true
	for _, part := range patternParts {
		next := make([]bool, len(nameParts)+1)
		if part == "**" {
			next[0] = matched[0]
			for nameIndex := 1; nameIndex < len(next); nameIndex++ {
				next[nameIndex] = matched[nameIndex] || next[nameIndex-1]
			}
		} else {
			for nameIndex := 1; nameIndex < len(next); nameIndex++ {
				if !matched[nameIndex-1] {
					continue
				}
				next[nameIndex], _ = path.Match(part, nameParts[nameIndex-1])
			}
		}
		matched = next
	}
	return matched[len(nameParts)], nil
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func splitSearchPath(value string) []string {
	value = strings.Trim(strings.ReplaceAll(value, "\\", "/"), "/")
	if value == "" || value == "." {
		return nil
	}
	return strings.Split(value, "/")
}

func truncateMatchLine(value string) string {
	runeCount := 0
	for byteIndex := range value {
		if runeCount == maxMatchLineRunes {
			return value[:byteIndex] + "..."
		}
		runeCount++
	}
	return value
}
