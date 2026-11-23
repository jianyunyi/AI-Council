package approval

import (
	"testing"

	"github.com/aicouncil/aicouncil/internal/council/schema"
)

func testSigner(t *testing.T) *Signer {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	s, err := NewSigner(key)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return s
}

func TestSignerVerifiesMatchingSignature(t *testing.T) {
	s := testSigner(t)
	plan := schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "go", Args: []string{"test"}}}}
	sig, err := s.Sign("run", "ws", 1, plan, 1 /*expires*/, "nonce-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(sig, "run", "ws", 1, plan, 1, "nonce-1"); err != nil {
		t.Fatalf("verify valid signature: %v", err)
	}
}

func TestSignerRejectsTamperedFields(t *testing.T) {
	s := testSigner(t)
	plan := schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "echo", Args: []string{"ok"}}}}
	sig, _ := s.Sign("run", "ws", 1, plan, 100, "nonce-1")
	tampered := schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "echo", Args: []string{"tampered"}}}}
	if err := s.Verify(sig, "run", "ws", 1, tampered, 100, "nonce-1"); err == nil {
		t.Fatal("tampered plan accepted")
	}
	if err := s.Verify(sig, "run", "ws", 1, plan, 100, "nonce-2"); err == nil {
		t.Fatal("swapped nonce accepted")
	}
	if err := s.Verify(sig, "run", "ws", 1, plan, 999, "nonce-1"); err == nil {
		t.Fatal("changed expiry accepted")
	}
}

func TestSignerDifferentKeyFails(t *testing.T) {
	a := testSigner(t)
	other := make([]byte, 32)
	other[0] = 0xFF
	b, _ := NewSigner(other)
	plan := schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "go"}}}
	sig, _ := a.Sign("run", "ws", 1, plan, 100, "n")
	if err := b.Verify(sig, "run", "ws", 1, plan, 100, "n"); err == nil {
		t.Fatal("signature verified under a different key")
	}
}
