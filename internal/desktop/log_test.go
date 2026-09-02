package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotatingLogWriterBoundsCurrentAndPreviousLogs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner.log")
	writer, err := newRotatingLogWriter(path, 8)
	if err != nil {
		t.Fatalf("newRotatingLogWriter() error = %v", err)
	}
	if _, err := writer.Write([]byte("first\n")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := writer.Write([]byte("second\n")); err != nil {
		t.Fatalf("second write: %v", err)
	}
	previous, err := os.ReadFile(path + ".1")
	if err != nil || string(previous) != "first\n" {
		t.Fatalf("previous log = %q, %v", previous, err)
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != "second\n" {
		t.Fatalf("current log = %q, %v", current, err)
	}
}
