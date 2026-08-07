package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

const modulePath = "github.com/KyrieWang7/nous-agent/agent-go"

func main() {
	deps, err := loadDeps()
	if err != nil {
		fmt.Fprintf(os.Stderr, "layercheck: %v\n", err)
		os.Exit(1)
	}

	violations := check(deps)
	if len(violations) == 0 {
		fmt.Printf("layercheck: %d packages, no layering violations\n", len(deps))
		return
	}

	fmt.Fprintf(os.Stderr, "layercheck: %d layering violation(s)\n\n", len(violations))
	for _, v := range violations {
		fmt.Fprintln(os.Stderr, v)
		fmt.Fprintln(os.Stderr)
	}
	os.Exit(1)
}

// goListPackage 是 `go list -json` 输出里我们关心的部分。
type goListPackage struct {
	ImportPath string   `json:"ImportPath"`
	Deps       []string `json:"Deps"`
	Standard   bool     `json:"Standard"`
}

// loadDeps 返回本模块内每个包 → 其模块内全部（含间接）依赖。
func loadDeps() (map[string][]string, error) {
	cmd := exec.Command("go", "list", "-deps=false", "-json", "./...")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list failed: %w", err)
	}

	deps := make(map[string][]string)
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var p goListPackage
		if err := dec.Decode(&p); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decoding go list output: %w", err)
		}
		if p.Standard {
			continue
		}

		local := relative(p.ImportPath)
		if local == "" {
			continue
		}

		var localDeps []string
		for _, d := range p.Deps {
			if rel := relative(d); rel != "" {
				localDeps = append(localDeps, rel)
			}
		}
		deps[local] = localDeps
	}

	if len(deps) == 0 {
		return nil, fmt.Errorf("go list returned no packages for %s", modulePath)
	}
	return deps, nil
}

// relative 把完整 import path 转成相对模块根的路径。模块外的包返回空串。
func relative(importPath string) string {
	if importPath == modulePath {
		return "."
	}
	prefix := modulePath + "/"
	if !strings.HasPrefix(importPath, prefix) {
		return ""
	}
	return strings.TrimPrefix(importPath, prefix)
}
