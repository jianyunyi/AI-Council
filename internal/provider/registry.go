package provider

import "context"

type Registry struct {
	providers map[string]ModelProvider
}

func NewRegistry(items ...ModelProvider) *Registry {
	registry := &Registry{providers: make(map[string]ModelProvider, len(items))}
	for _, item := range items {
		registry.providers[item.Name()] = item
	}
	return registry
}

func (r *Registry) Get(name string) (ModelProvider, bool) {
	item, ok := r.providers[name]
	return item, ok
}

// Named wraps a provider so the registry keys it under name instead of the
// vendor's own Name(). Role assignments key providers by profile ID, so two
// profiles of the same vendor can coexist in one registry.
func Named(name string, p ModelProvider) ModelProvider {
	if p == nil {
		return nil
	}
	return namedProvider{name: name, inner: p}
}

type namedProvider struct {
	name  string
	inner ModelProvider
}

func (p namedProvider) Name() string { return p.name }

func (p namedProvider) Generate(ctx context.Context, req Request) (Response, error) {
	return p.inner.Generate(ctx, req)
}
