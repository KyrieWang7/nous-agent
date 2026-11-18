package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

var modulePath = "github.com/KyrieWang7/nous-agent/agent-go"

func main() {
	moduleDir := flag.String("dir", ".", "Go module directory to check")
	flag.Parse()
	if err := os.Chdir(*moduleDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := rejectRemovedDirectories("pkg/middleware", "internal/transport", "internal/httpapi", "internal/store", "internal/catalog", "internal/files", "internal/convert"); err != nil {
		fmt.Fprintf(os.Stderr, "layercheck: %v\n", err)
		os.Exit(1)
	}
	if err := rejectRemovedFiles("internal/transport/httpapi/harness.go"); err != nil {
		fmt.Fprintf(os.Stderr, "layercheck: %v\n", err)
		os.Exit(1)
	}
	deps, direct, err := loadDeps()
	if err != nil {
		fmt.Fprintf(os.Stderr, "layercheck: %v\n", err)
		os.Exit(1)
	}

	violations := append(check(deps), checkRules(direct, applicationRules)...)
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

func rejectRemovedFiles(paths ...string) error {
	for _, path := range paths {
		info, err := os.Stat(path)
		switch {
		case err == nil && !info.IsDir():
			return fmt.Errorf("removed architecture file %s exists", path)
		case err == nil:
			return fmt.Errorf("removed architecture path %s exists as a directory", path)
		case !os.IsNotExist(err):
			return fmt.Errorf("checking removed architecture file %s: %w", path, err)
		}
	}
	return nil
}

func rejectRemovedDirectories(paths ...string) error {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("removed architecture directory %s exists", path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("checking removed architecture directory %s: %w", path, err)
		}
	}
	return nil
}

// goListPackage 是 `go list -json` 输出里我们关心的部分。
type goListPackage struct {
	ImportPath string   `json:"ImportPath"`
	Deps       []string `json:"Deps"`
	Imports    []string `json:"Imports"`
	Module     *struct {
		Path string `json:"Path"`
	} `json:"Module"`
	Standard bool `json:"Standard"`
}

// loadDeps 返回本模块内每个包 → 其全部（含间接）依赖。模块内依赖使用
// 相对路径，标准库和第三方依赖放入 external/ 命名空间，避免把标准库的
// internal/* 误认成本模块 internal/*，同时允许规则识别具体外部依赖。
func loadDeps() (map[string][]string, map[string][]string, error) {
	cmd := exec.Command("go", "list", "-deps=false", "-json", "./...")
	out, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("go list failed: %w", err)
	}

	deps := make(map[string][]string)
	direct := make(map[string][]string)
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var p goListPackage
		if err := dec.Decode(&p); err != nil {
			if err == io.EOF {
				break
			}
			return nil, nil, fmt.Errorf("decoding go list output: %w", err)
		}
		if p.Standard {
			continue
		}

		if p.Module != nil {
			modulePath = p.Module.Path
		}
		local := relative(p.ImportPath)
		if local == "" {
			continue
		}

		var packageDeps []string
		for _, d := range p.Deps {
			if rel := relative(d); rel != "" {
				packageDeps = append(packageDeps, rel)
			} else {
				packageDeps = append(packageDeps, "external/"+d)
			}
		}
		deps[local] = packageDeps
		for _, dep := range p.Imports {
			if rel := relative(dep); rel != "" {
				direct[local] = append(direct[local], rel)
			} else {
				direct[local] = append(direct[local], "external/"+dep)
			}
		}
	}

	if len(deps) == 0 {
		return nil, nil, fmt.Errorf("go list returned no packages for %s", modulePath)
	}
	return deps, direct, nil
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
