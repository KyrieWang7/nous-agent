package middleware

import (
	"fmt"
	"slices"
	"strings"
)

// Build 把扩展中间件按声明的锚点插入基础链。
//
// 基础链由装配函数显式列出；扩展中间件（插件或 SDK 调用方提供）声明
// AnchorAfter / AnchorBefore。规则（设计文档 §4.3）：
//
//   - 同时声明 After 与 Before → 装配期报错。
//   - 两个扩展锚同一目标 → 冲突，报错。
//   - 锚点目标不存在 → 报错。
//   - 相互锚定成环 → 报错。
//   - 未声明锚点 → 插到终端中间件之前（没有终端则追加到末尾）。
//
// 全部错误在装配期返回，进程起不来。这是刻意的：nous-agent 的
// RuntimeFeatures 与 extra_middleware 都是"声明了但从未生效"的死缝，
// 根因是静默跳过。
func Build(base, extras []Middleware) ([]Middleware, error) {
	chain := slices.Clone(base)
	if len(extras) == 0 {
		return chain, nil
	}

	present := make(map[string]struct{}, len(chain))
	for _, m := range chain {
		present[m.Name()] = struct{}{}
	}

	var (
		anchoredExtras []Middleware
		unanchored     []Middleware
		afterClaims    = map[string]string{} // 目标 → 声明者
		beforeClaims   = map[string]string{}
	)

	for _, m := range extras {
		if m == nil {
			return nil, fmt.Errorf("middleware: nil entry in extras")
		}
		name := m.Name()
		if name == "" {
			return nil, fmt.Errorf("middleware: extra middleware has an empty name")
		}
		if _, dup := present[name]; dup {
			return nil, fmt.Errorf("middleware: duplicate name %q between the base chain and extras", name)
		}
		present[name] = struct{}{}

		a := anchorOf(m)
		switch {
		case a.After != "" && a.Before != "":
			return nil, fmt.Errorf("middleware: %q declares both After(%s) and Before(%s); they are mutually exclusive",
				name, a.After, a.Before)

		case a.After != "":
			if owner, taken := afterClaims[a.After]; taken {
				return nil, fmt.Errorf("middleware: conflict — %q and %q both anchor After(%s)", name, owner, a.After)
			}
			if owner, taken := beforeClaims[a.After]; taken {
				return nil, fmt.Errorf("middleware: conflict — %q anchors After(%s) while %q anchors Before(%s); anchor them to each other instead",
					name, a.After, owner, a.After)
			}
			afterClaims[a.After] = name
			anchoredExtras = append(anchoredExtras, m)

		case a.Before != "":
			if owner, taken := beforeClaims[a.Before]; taken {
				return nil, fmt.Errorf("middleware: conflict — %q and %q both anchor Before(%s)", name, owner, a.Before)
			}
			if owner, taken := afterClaims[a.Before]; taken {
				return nil, fmt.Errorf("middleware: conflict — %q anchors Before(%s) while %q anchors After(%s); anchor them to each other instead",
					name, a.Before, owner, a.Before)
			}
			beforeClaims[a.Before] = name
			anchoredExtras = append(anchoredExtras, m)

		default:
			unanchored = append(unanchored, m)
		}
	}

	chain = insertUnanchored(chain, unanchored)

	chain, err := insertAnchored(chain, anchoredExtras)
	if err != nil {
		return nil, err
	}
	return chain, nil
}

// insertUnanchored 把无锚点的扩展插到终端中间件之前。
func insertUnanchored(chain, extras []Middleware) []Middleware {
	if len(extras) == 0 {
		return chain
	}

	at := len(chain)
	for i, m := range chain {
		if m.Name() == TerminalName {
			at = i
			break
		}
	}

	out := make([]Middleware, 0, len(chain)+len(extras))
	out = append(out, chain[:at]...)
	out = append(out, extras...)
	out = append(out, chain[at:]...)
	return out
}

// insertAnchored 迭代插入有锚点的扩展，支持扩展锚定另一个扩展。
func insertAnchored(chain, extras []Middleware) ([]Middleware, error) {
	pending := slices.Clone(extras)

	for len(pending) > 0 {
		var stuck []Middleware

		for _, m := range pending {
			a := anchorOf(m)
			target := a.After
			if target == "" {
				target = a.Before
			}

			idx := indexOfName(chain, target)
			if idx < 0 {
				stuck = append(stuck, m)
				continue
			}
			at := idx
			if a.After != "" {
				at = idx + 1
			}
			chain = slices.Insert(chain, at, m)
		}

		if len(stuck) == len(pending) {
			return nil, unresolvableError(chain, stuck)
		}
		pending = stuck
	}

	return chain, nil
}

// unresolvableError 区分"成环"与"锚点不存在"，给出可操作的错误信息。
func unresolvableError(chain, stuck []Middleware) error {
	stuckNames := make(map[string]struct{}, len(stuck))
	for _, m := range stuck {
		stuckNames[m.Name()] = struct{}{}
	}

	var circular []string
	var missing []string
	for _, m := range stuck {
		a := anchorOf(m)
		target := a.After
		if target == "" {
			target = a.Before
		}
		if _, isStuck := stuckNames[target]; isStuck {
			circular = append(circular, fmt.Sprintf("%s→%s", m.Name(), target))
			continue
		}
		missing = append(missing, fmt.Sprintf("%s→%s", m.Name(), target))
	}

	if len(circular) > 0 {
		return fmt.Errorf("middleware: circular anchors among extras: %s", strings.Join(circular, ", "))
	}
	return fmt.Errorf("middleware: unresolvable anchors %s; available middlewares: %v",
		strings.Join(missing, ", "), namesOf(chain))
}

// VerifyPresent 断言 declared 里的每个名字都出现在链里。
//
// 配置声明启用了某个中间件，它就必须真的在链里。这条断言存在的唯一目的
// 是让"声明了但从未生效"这类死缝在启动时暴露，而不是上线几个月后
// 才发现某个治理中间件一直没跑。
func VerifyPresent(chain []Middleware, declared []string) error {
	present := make(map[string]struct{}, len(chain))
	for _, m := range chain {
		present[m.Name()] = struct{}{}
	}

	var absent []string
	for _, name := range declared {
		if _, ok := present[name]; !ok {
			absent = append(absent, name)
		}
	}
	if len(absent) == 0 {
		return nil
	}
	return fmt.Errorf("middleware: declared but missing from the chain: %v; chain contains: %v",
		absent, namesOf(chain))
}

func anchorOf(m Middleware) Anchor {
	if a, ok := m.(Anchored); ok {
		return a.Anchor()
	}
	return Anchor{}
}

func indexOfName(chain []Middleware, name string) int {
	for i, m := range chain {
		if m.Name() == name {
			return i
		}
	}
	return -1
}

func namesOf(chain []Middleware) []string {
	out := make([]string, len(chain))
	for i, m := range chain {
		out[i] = m.Name()
	}
	return out
}
