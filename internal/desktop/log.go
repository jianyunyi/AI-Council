package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const defaultLogLimit int64 = 1 << 20

// rotatingLogWriter retains the current log and one bounded predecessor.
// Child-process output must always use this writer rather than inheriting the
// desktop process console.
type rotatingLogWriter struct {
	mu    sync.Mutex
	path  string
	limit int64
	size  int64
}

func newRotatingLogWriter(path string, limit int64) (*rotatingLogWriter, error) {
	if limit <= 0 {
		limit = defaultLogLimit
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat log file: %w", err)
	}
	writer := &rotatingLogWriter{path: path, limit: limit}
	if err == nil {
		writer.size = info.Size()
	}
	return writer, nil
}

func (w *rotatingLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size > 0 && w.size+int64(len(p)) > w.limit {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	file, err := os.OpenFile(w.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	n, writeErr := file.Write(p)
	closeErr := file.Close()
	if writeErr != nil {
		return n, writeErr
	}
	if closeErr != nil {
		return n, closeErr
	}
	w.size += int64(n)
	return n, nil
}

func (w *rotatingLogWriter) rotate() error {
	previous := w.path + ".1"
	if err := os.Remove(previous); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(w.path, previous); err != nil && !os.IsNotExist(err) {
		return err
	}
	w.size = 0
	return nil
}
