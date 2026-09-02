package main

import (
	"strings"
	"testing"

	"github.com/aicouncil/aicouncil/internal/desktop"
)

func TestDesktopAppPersistsProviderKeyWithoutExposingIt(t *testing.T) {
	dataDir := t.TempDir()
	store, err := desktop.NewSecretStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	app, err := NewDesktopApp(desktop.Config{DataDir: dataDir}, store, dataDir)
	if err != nil {
		t.Fatal(err)
	}

	if err := app.SaveProviderKey("openai", "provider-secret"); err != nil {
		t.Fatalf("SaveProviderKey() error = %v", err)
	}
	if err := app.SaveProviderKey("unknown", "provider-secret"); err == nil {
		t.Fatal("SaveProviderKey() accepted an unknown provider")
	}
	if got := app.Status(); strings.Contains(got.LastError+got.Workspace+got.CouncilURL, "provider-secret") {
		t.Fatalf("Status leaked a provider key: %#v", got)
	}
}
