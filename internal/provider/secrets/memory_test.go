package secrets

import (
	"reflect"
	"sync"
	"testing"
)

func TestMemoryVaultLifecycle(t *testing.T) {
	vault := NewMemoryVault()
	vault.Put("profile-b", "sk-second")
	vault.Put("profile-a", "sk-first")

	value, ok := vault.Get("profile-a")
	if !ok || value != "sk-first" {
		t.Fatalf("Get(profile-a) = %q, %v, want sk-first, true", value, ok)
	}
	if got := vault.IDs(); !reflect.DeepEqual(got, []string{"profile-a", "profile-b"}) {
		t.Fatalf("IDs() = %#v, want sorted profile IDs", got)
	}

	vault.Put("profile-a", "sk-replaced")
	value, _ = vault.Get("profile-a")
	if value != "sk-replaced" {
		t.Fatalf("Get(profile-a) after replace = %q", value)
	}
	vault.Delete("profile-a")
	if _, ok := vault.Get("profile-a"); ok {
		t.Fatal("Get(profile-a) after Delete ok = true, want false")
	}
}

func TestMemoryVaultSupportsConcurrentAccess(t *testing.T) {
	vault := NewMemoryVault()
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			vault.Put("profile", "secret")
			_, _ = vault.Get("profile")
			_ = vault.IDs()
		}()
	}
	group.Wait()
}
