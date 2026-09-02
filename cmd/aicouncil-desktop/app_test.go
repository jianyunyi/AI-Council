package main

import (
	"os"
	"path/filepath"
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

func TestDesktopAppExportsSanitizedDiagnostics(t *testing.T) {
	dataDir := t.TempDir()
	logDir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "runner.log"), []byte("token=not-for-support"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := desktop.NewSecretStore(filepath.Join(dataDir, "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	app, err := NewDesktopApp(desktop.Config{DataDir: dataDir, LogDir: logDir}, store, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := app.ExportDiagnostics(filepath.Join(dataDir, "exports"))
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "not-for-support") {
		t.Fatal("diagnostic archive leaked a token")
	}
}
