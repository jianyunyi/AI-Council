//go:build !windows

package desktop

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
)

// Non-Windows development uses a private file. Production Windows builds use
// DPAPI in masterkey_windows.go so that copying the data directory is not
// enough to decrypt provider credentials.
func loadOrCreateMasterKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != 32 {
			return nil, fmt.Errorf("secret store master key has invalid length %d", len(key))
		}
		return key, os.Chmod(path, 0o600)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read secret store master key: %w", err)
	}
	key = make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate secret store master key: %w", err)
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("write secret store master key: %w", err)
	}
	return key, nil
}
