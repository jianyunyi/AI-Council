//go:build windows

package desktop

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"unsafe"
)

const cryptProtectUIForbidden = 0x1

var (
	crypt32            = syscall.NewLazyDLL("crypt32.dll")
	cryptProtectData   = crypt32.NewProc("CryptProtectData")
	cryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	localFree          = kernel32.NewProc("LocalFree")
)

type dataBlob struct {
	Size uint32
	Data *byte
}

func loadOrCreateMasterKey(path string) ([]byte, error) {
	protected, err := os.ReadFile(path)
	if err == nil {
		key, err := unprotectForCurrentUser(protected)
		if err != nil {
			return nil, fmt.Errorf("unprotect secret store master key: %w", err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("secret store master key has invalid length %d", len(key))
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read secret store master key: %w", err)
	}

	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate secret store master key: %w", err)
	}
	protected, err = protectForCurrentUser(key)
	if err != nil {
		return nil, fmt.Errorf("protect secret store master key: %w", err)
	}
	if err := os.WriteFile(path, protected, 0o600); err != nil {
		return nil, fmt.Errorf("write protected secret store master key: %w", err)
	}
	return key, nil
}

func protectForCurrentUser(value []byte) ([]byte, error) {
	return cryptData(cryptProtectData, value)
}

func unprotectForCurrentUser(value []byte) ([]byte, error) {
	return cryptData(cryptUnprotectData, value)
}

func cryptData(proc *syscall.LazyProc, value []byte) ([]byte, error) {
	if len(value) == 0 {
		return nil, errors.New("DPAPI input is empty")
	}
	in := dataBlob{Size: uint32(len(value)), Data: &value[0]}
	var out dataBlob
	r1, _, callErr := proc.Call(
		uintptr(unsafe.Pointer(&in)),
		0,
		0,
		0,
		0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r1 == 0 {
		if callErr != syscall.Errno(0) {
			return nil, callErr
		}
		return nil, errors.New("DPAPI call failed")
	}
	defer localFree.Call(uintptr(unsafe.Pointer(out.Data)))
	if out.Size == 0 || out.Data == nil {
		return nil, errors.New("DPAPI returned empty output")
	}
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}
