package desktop

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportDiagnosticsRedactsSecretsAndBoundsLogs(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "runner.log"), []byte("Authorization: Bearer session-token\napi_key=provider-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "council.log"), []byte(strings.Repeat("x", diagnosticLogLimit+100)), 0o600); err != nil {
		t.Fatal(err)
	}

	archive, err := ExportDiagnostics(filepath.Join(root, "exports"), "0.1.0", Config{DataDir: root, LogDir: logDir}, RuntimeStatus{State: StateReady, CouncilURL: "http://127.0.0.1:12345"})
	if err != nil {
		t.Fatal(err)
	}
	entries := readDiagnosticArchive(t, archive)
	if strings.Contains(entries["runner.log"], "session-token") || strings.Contains(entries["runner.log"], "provider-secret") {
		t.Fatalf("diagnostics leaked secret: %q", entries["runner.log"])
	}
	if len(entries["council.log"]) > diagnosticLogLimit {
		t.Fatalf("council log size = %d, want at most %d", len(entries["council.log"]), diagnosticLogLimit)
	}
	if !strings.Contains(entries["summary.json"], `"version":"0.1.0"`) || strings.Contains(entries["summary.json"], "session-token") {
		t.Fatalf("summary = %q", entries["summary.json"])
	}
}

func readDiagnosticArchive(t *testing.T, path string) map[string]string {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	entries := make(map[string]string)
	for _, file := range reader.File {
		contents, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(contents)
		closeErr := contents.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		entries[file.Name] = string(data)
	}
	return entries
}
