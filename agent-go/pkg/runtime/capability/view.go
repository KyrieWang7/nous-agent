package capability

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// View is an immutable allowlisted projection of one generation snapshot.
// Restrict only accepts subsets, so child runtime code cannot widen a parent
// capability set by constructing a new list of names.
type View struct {
	snapshot Snapshot
	allowed  map[string]struct{}
}

func NewView(snapshot Snapshot, allowed []string) (View, error) {
	if allowed == nil {
		allowed = snapshot.Names()
	}
	view := View{snapshot: snapshot, allowed: make(map[string]struct{}, len(allowed))}
	for _, name := range allowed {
		if _, exists := view.allowed[name]; exists {
			continue
		}
		if _, err := snapshot.Get(name); err != nil {
			return View{}, fmt.Errorf("capability view: allowlisted name %q is not in the generation: %w", name, err)
		}
		view.allowed[name] = struct{}{}
	}
	return view, nil
}

func (v View) Empty() bool { return len(v.allowed) == 0 }

func (v View) Initialized() bool { return v.allowed != nil }

func (v View) Names() []string {
	names := make([]string, 0, len(v.allowed))
	for name := range v.allowed {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (v View) Restrict(names []string) (View, error) {
	if v.allowed == nil {
		return View{}, errors.New("capability view: uninitialized parent view")
	}
	for _, name := range names {
		if _, ok := v.allowed[name]; !ok {
			return View{}, fmt.Errorf("capability view: %q would widen the parent allowlist", name)
		}
	}
	return NewView(v.snapshot, names)
}

func (v View) Resolve(ctx context.Context, name string, kind Kind, req ResolveRequest) (any, error) {
	if _, ok := v.allowed[name]; !ok {
		return nil, fmt.Errorf("capability view: %q is not allowed", name)
	}
	return v.snapshot.Resolve(ctx, name, kind, req)
}

func ResolveViewAs[T any](ctx context.Context, view View, name string, kind Kind, req ResolveRequest) (T, error) {
	var zero T
	value, err := view.Resolve(ctx, name, kind, req)
	if err != nil {
		return zero, err
	}
	typed, ok := value.(T)
	if !ok {
		return zero, fmt.Errorf("capability %q: resolved %T, want requested domain type", name, value)
	}
	return typed, nil
}
