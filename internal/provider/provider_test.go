package provider

import (
	"context"
	"testing"
)

type stubProvider struct{ name string }

func (p stubProvider) Name() string { return p.name }
func (p stubProvider) Generate(context.Context, Request) (Response, error) {
	return Response{Content: []byte(`{"ok":true}`)}, nil
}

func TestRegistryReturnsProviderByName(t *testing.T) {
	registry := NewRegistry(stubProvider{name: "openai"}, stubProvider{name: "anthropic"})
	item, ok := registry.Get("anthropic")
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if item.Name() != "anthropic" {
		t.Fatalf("provider name = %q, want anthropic", item.Name())
	}
	if _, ok := registry.Get("missing"); ok {
		t.Fatal("Get(missing) ok = true, want false")
	}
}
