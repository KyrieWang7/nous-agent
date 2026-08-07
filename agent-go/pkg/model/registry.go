package model

import (
	"fmt"
	"slices"
	"sync"
)

// Factory 按配置构造一个 Model 实例。
type Factory func(ProviderConfig) (Model, error)

// Registry 是供应商的编译期注册表。
//
// Go 没有运行时类加载，Python 的 dotted-path 反射（nous-agent 的
// src/reflection/resolvers.py）无法平移。替代方案是：实现在 init() 里注册，
// 配置按名字引用，启动时校验名字都已注册 —— 未知名字直接失败，
// 绝不静默跳过（设计文档 §16）。
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// NewRegistry 返回空注册表。
func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

// Register 注册一个供应商工厂。重名、空名与 nil 工厂都返回 error。
func (r *Registry) Register(name string, f Factory) error {
	if name == "" {
		return fmt.Errorf("model: provider name must not be empty")
	}
	if f == nil {
		return fmt.Errorf("model: provider %q factory must not be nil", name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.factories[name]; exists {
		return fmt.Errorf("model: provider %q already registered", name)
	}
	r.factories[name] = f
	return nil
}

// Get 返回已注册的工厂。未知名字返回列出可用名字的 error ——
// 让配置写错的人一眼看到该写什么，而不是拿到一个语焉不详的 nil。
func (r *Registry) Get(name string) (Factory, error) {
	r.mu.RLock()
	f, ok := r.factories[name]
	r.mu.RUnlock()

	if ok {
		return f, nil
	}
	return nil, fmt.Errorf("model: unknown provider %q; registered providers: %v", name, r.Names())
}

// Build 按配置构造 Model。
func (r *Registry) Build(cfg ProviderConfig) (Model, error) {
	f, err := r.Get(cfg.Name)
	if err != nil {
		return nil, err
	}
	m, err := f(cfg)
	if err != nil {
		return nil, fmt.Errorf("model: building provider %q: %w", cfg.Name, err)
	}
	return m, nil
}

// Names 返回已注册的供应商名字，按字典序排序（便于稳定的错误信息与测试）。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
