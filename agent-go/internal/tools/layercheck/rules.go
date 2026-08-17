// Command layercheck 断言设计文档 §2 的依赖纪律。违反即非零退出。
//
// 分层纪律靠人工 review 守不住：一个无心的 import 就能让内核依赖上层，
// 而这种耦合在功能测试里完全看不出来，只会在几个月后表现为"改不动"。
package main

import (
	"fmt"
	"slices"
	"strings"
)

// rule 是一条禁止依赖规则。
type rule struct {
	// from 是包路径后缀（相对模块根，如 "pkg/loop"）。
	from string

	// forbidden 是 from 不得（直接或间接）依赖的包路径后缀。
	forbidden []string

	// why 解释这条规则存在的原因，出现在失败信息里。
	why string

	// exceptFrom 列出豁免的具体包（用于 from 是前缀匹配时的例外）。
	exceptFrom []string
}

// rules 是全部纪律，逐条对应设计文档 §2「依赖纪律」。
var rules = []rule{
	{
		from:      "pkg/message",
		forbidden: []string{"pkg/loop", "pkg/runtime/lifecycle", "pkg/harness", "pkg/tool", "pkg/model"},
		why:       "转录是最底层的数据结构，不得依赖编排或上层抽象",
	},
	{
		from:      "pkg/model",
		forbidden: []string{"pkg/loop", "pkg/runtime/lifecycle", "pkg/harness", "pkg/tool"},
		why:       "模型层收敛为单一接口，供应商适配到它，不反向依赖运行时",
	},
	{
		from:      "pkg/tool",
		forbidden: []string{"pkg/loop", "pkg/runtime/lifecycle", "pkg/harness"},
		why:       "工具层只暴露 tool.Interceptor，不得反向依赖内核运行周期（会成环）",
	},
	{
		from:      "pkg/runtime/lifecycle",
		forbidden: []string{"pkg/loop", "pkg/harness"},
		why:       "生命周期处理器不拥有编排，编排只归 pkg/loop",
	},
	{
		from:       "pkg/runtime/lifecycle",
		forbidden:  []string{"pkg/runtime/lifecycle/handlers"},
		why:        "契约不得依赖实现",
		exceptFrom: []string{"pkg/runtime/lifecycle/handlers"},
	},
	{
		from: "pkg/loop",
		forbidden: []string{
			"pkg/harness", "pkg/config", "internal/transport/httpapi",
			"pkg/runtime/postgres", "pkg/runtime/redis",
			"pkg/model/provider", "pkg/tool/builtin", "pkg/tool/community",
			"pkg/sandbox/local", "pkg/sandbox/remote",
			"external/net/http", "external/database/sql",
			"external/github.com/jackc/pgx", "external/github.com/redis/go-redis",
		},
		why: "内核只吃 typed contracts，不认识配置、wire、数据库驱动或 capability 具体实现",
	},
	{
		from:      "pkg/prompt",
		forbidden: []string{"pkg/loop", "pkg/runtime/lifecycle", "pkg/harness"},
		why:       "提示装配是纯函数层",
	},
	{
		from:      "pkg/sandbox",
		forbidden: []string{"pkg/permission", "pkg/loop", "pkg/runtime/lifecycle"},
		why:       "沙箱只做隔离，不得生长出审批或权限判定职责",
	},
	{
		from:      "pkg",
		forbidden: []string{"internal/"},
		why:       "库不得依赖 internal；wire 格式适配是单向的",
	},
	{
		from: "internal/transport",
		forbidden: []string{
			"pkg/loop", "pkg/harness", "pkg/config",
			"pkg/model/provider", "pkg/tool/builtin", "pkg/tool/community",
			"pkg/sandbox/local", "pkg/sandbox/remote",
		},
		why: "Transport 只适配 Runtime contract 和 wire projection，不得装配 Kernel 或具体 capability",
	},
	{
		from: "pkg/runtime/runmanager",
		forbidden: []string{
			"internal/transport", "pkg/config", "pkg/harness", "pkg/loop",
			"pkg/runtime/postgres", "pkg/runtime/redis",
			"pkg/model/provider", "pkg/tool/builtin", "pkg/tool/community",
			"pkg/sandbox/local", "pkg/sandbox/remote",
		},
		why: "RunManager 是 transport-neutral Runtime 编排边界，只能依赖 Kernel contract 和 persistence port",
	},
	{
		from: "pkg/runtime/capability",
		forbidden: []string{
			"internal/transport", "pkg/config", "pkg/harness", "pkg/loop",
			"pkg/runtime/postgres", "pkg/runtime/redis",
		},
		why: "Capability contract 不得反向依赖产品、Kernel 或 persistence implementation",
	},
	{
		from: "pkg/runtime/metadata",
		forbidden: []string{
			"internal/transport", "pkg/config", "pkg/harness", "pkg/loop",
			"pkg/runtime/runmanager", "pkg/runtime/postgres", "pkg/runtime/redis",
		},
		why: "Metadata persistence port 是底层 Runtime contract，不得反向依赖编排、Transport 或具体实现",
	},
	{
		from:      "pkg/subagent",
		forbidden: []string{"pkg/runtime/redis", "external/github.com/redis/go-redis"},
		why:       "Subagent capability 只依赖 TaskStore port，不得内置 Redis persistence implementation",
	},
	{
		from:      "pkg/memory",
		forbidden: []string{"pkg/runtime/postgres", "external/github.com/jackc/pgx"},
		why:       "Memory capability 只依赖 Store port，不得内置 PostgreSQL persistence implementation",
	},
}

// violation 是一条被违反的规则实例。
type violation struct {
	pkg    string
	dep    string
	why    string
	viaAll []string
}

func (v violation) String() string {
	s := fmt.Sprintf("  %s\n    → 不得依赖 %s\n    原因：%s", v.pkg, v.dep, v.why)
	if len(v.viaAll) > 0 {
		s += fmt.Sprintf("\n    依赖链：%s", strings.Join(v.viaAll, " → "))
	}
	return s
}

// check 对一组包及其依赖求解违规。
//
// deps 是包 → 其全部（含间接）依赖的映射，键与值都是相对模块根的路径。
func check(deps map[string][]string) []violation {
	var out []violation

	pkgs := make([]string, 0, len(deps))
	for p := range deps {
		pkgs = append(pkgs, p)
	}
	slices.Sort(pkgs)

	for _, pkg := range pkgs {
		for _, r := range rules {
			if !appliesTo(r, pkg) {
				continue
			}
			for _, dep := range deps[pkg] {
				if forbids(r, pkg, dep) {
					out = append(out, violation{pkg: pkg, dep: dep, why: r.why})
				}
			}
		}
	}
	return out
}

// appliesTo 报告规则是否作用于该包。
func appliesTo(r rule, pkg string) bool {
	if !underPath(pkg, r.from) {
		return false
	}
	for _, ex := range r.exceptFrom {
		if underPath(pkg, ex) {
			return false
		}
	}
	return true
}

// forbids 报告 dep 是否被规则禁止。
func forbids(r rule, pkg, dep string) bool {
	for _, f := range r.forbidden {
		f = strings.TrimSuffix(f, "/")
		if !underPath(dep, f) {
			continue
		}

		// 显式点名自身子包的规则优先于"可依赖自身子包"的豁免。
		// 「契约不得依赖实现」（lifecycle ↛ lifecycle/builtin）
		// 正是这种情况，若被豁免吞掉这条纪律就形同不存在。
		if f != pkg && underPath(f, pkg) {
			return true
		}

		// 否则包可以依赖自身子包（pkg/tool → pkg/tool/builtin 是允许的）。
		if underPath(dep, pkg) {
			continue
		}
		return true
	}
	return false
}

// underPath 报告 p 是否等于 prefix 或位于其之下。
//
// 用路径段而不是字符串前缀比较：否则 "pkg/tooling" 会被当成 "pkg/tool" 的子包。
func underPath(p, prefix string) bool {
	if prefix == "" {
		return false
	}
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}
