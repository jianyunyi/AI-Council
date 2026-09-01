package provider

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
