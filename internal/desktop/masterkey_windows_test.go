//go:build windows

package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsMasterKeyIsDPAPIProtected(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewSecretStore(dir); err != nil {
		t.Fatalf("NewSecretStore() error = %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(dir, secretMasterKeyFile))
	if err != nil {
		t.Fatalf("read protected master key: %v", err)
	}
	if len(contents) == 32 {
		t.Fatal("master key is stored as raw plaintext instead of DPAPI ciphertext")
	}
}
