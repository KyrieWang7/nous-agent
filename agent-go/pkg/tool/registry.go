package tool

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

// Registry 是工具的编译期注册表。
//
// 与 model.Registry 同一套纪律：配置按名字引用，未知名字立刻失败并列出可用名字，
// 绝不静默跳过（设计文档 §16）。
type Registry struct {
	mu    sync.RWMutex
	defs  map[string]Definition
	order []string
}

// NewRegistry 返回空注册表。
func NewRegistry() *Registry {
	return &Registry{defs: make(map[string]Definition)}
}

// Register 注册一个工具。校验失败或重名都返回 error。
func (r *Registry) Register(d Definition) error {
	if err := validate(d); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.defs[d.Name]; exists {
		return fmt.Errorf("tool: %q already registered", d.Name)
	}
	r.defs[d.Name] = d
	r.order = append(r.order, d.Name)
	slices.Sort(r.order)
	return nil
}

// RegisterAll 注册一批工具，遇到第一个错误即停。
func (r *Registry) RegisterAll(defs ...Definition) error {
	for _, d := range defs {
		if err := r.Register(d); err != nil {
			return err
		}
	}
	return nil
}

func validate(d Definition) error {
	if d.Name == "" {
		return fmt.Errorf("tool: name must not be empty")
	}
	if d.Group == "" {
		return fmt.Errorf("tool: %q must declare a group", d.Name)
	}
	if d.Handler == nil {
		return fmt.Errorf("tool: %q must provide a handler", d.Name)
	}
	if len(d.Parameters) > 0 && !json.Valid(d.Parameters) {
		return fmt.Errorf("tool: %q has invalid parameters JSON schema", d.Name)
	}
	if d.Metadata.Destructive {
		return fmt.Errorf(
			"tool: %q is marked destructive and must not be registered as an agent-callable tool; "+
				"route it through a human approval flow instead", d.Name)
	}
	return nil
}

// Get 返回已注册的定义。未知名字返回列出可用名字的 error。
func (r *Registry) Get(name string) (Definition, error) {
	r.mu.RLock()
	d, ok := r.defs[name]
	r.mu.RUnlock()

	if ok {
		return d, nil
	}
	return Definition{}, fmt.Errorf("tool: unknown tool %q; registered tools: %v", name, r.Names())
}

// Has 报告某个工具是否已注册。
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.defs[name]
	return ok
}

// Names 返回全部已注册工具名，字典序。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Clone(r.order)
}

// NamesInGroup 返回某个组内的工具名，字典序。
func (r *Registry) NamesInGroup(group string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []string
	for _, name := range r.order {
		if r.defs[name].Group == group {
			out = append(out, name)
		}
	}
	return out
}

// Schemas 返回投递给模型的工具 schema。
//
// allow 是本轮的白名单（权限 ∩ 组 ∩ skill 收窄的结果，见设计文档 §7.3）。
// disclosed 列出被已激活 skill 显式披露的延迟工具。
//
// 两条不变量：
//   - 未披露的 Deferred 工具不出现在结果里。
//   - 披露不能放大白名单：disclosed 里但不在 allow 里的工具依然不出现。
//     否则 skill 就成了绕过权限的后门。
//
// 结果按注册表的字典序返回，不随 allow 的顺序变化 —— 请求体逐字节稳定才能命中
// 供应商侧的 prompt 缓存。
func (r *Registry) Schemas(allow []string, disclosed ...string) []model.ToolSchema {
	allowed := make(map[string]struct{}, len(allow))
	for _, name := range allow {
		allowed[name] = struct{}{}
	}
	shown := make(map[string]struct{}, len(disclosed))
	for _, name := range disclosed {
		shown[name] = struct{}{}
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []model.ToolSchema
	for _, name := range r.order {
		if _, ok := allowed[name]; !ok {
			continue
		}
		d := r.defs[name]
		if d.Deferred {
			if _, ok := shown[name]; !ok {
				continue
			}
		}
		out = append(out, d.ModelSchema())
	}
	return out
}
