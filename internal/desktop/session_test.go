package desktop

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestSessionTokenIsUniqueBase64URLWithAtLeast32RandomBytes(t *testing.T) {
	first, err := NewSessionToken()
	if err != nil {
		t.Fatalf("first NewSessionToken() error = %v", err)
	}
	second, err := NewSessionToken()
	if err != nil {
		t.Fatalf("second NewSessionToken() error = %v", err)
	}

	if first == second {
		t.Fatal("NewSessionToken() returned duplicate tokens")
	}
	for _, token := range []string{first, second} {
		if strings.Contains(token, "=") {
			t.Errorf("token %q contains base64 padding", token)
		}
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(token)
		if decodeErr != nil {
			t.Fatalf("token %q is not raw base64url: %v", token, decodeErr)
		}
		if len(decoded) < 32 {
			t.Errorf("decoded token length = %d bytes, want at least 32", len(decoded))
		}
	}
}
