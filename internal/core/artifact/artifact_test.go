package artifact

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewProducesStableHashForEquivalentMaps(t *testing.T) {
	firstContent := map[string]any{
		"name": "Ada",
		"role": "chair",
	}
	secondContent := map[string]any{}
	secondContent["role"] = "chair"
	secondContent["name"] = "Ada"

	first, err := New("proposal", "proposal-1", "", firstContent)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	second, err := New("proposal", "proposal-1", "", secondContent)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if first.ContentHash != second.ContentHash {
		t.Fatalf("equivalent map hashes differ: %q != %q", first.ContentHash, second.ContentHash)
	}
}

func TestCurrentSchemaVersion(t *testing.T) {
	if CurrentSchemaVersion != "1.0" {
		t.Fatalf("CurrentSchemaVersion = %q, want %q", CurrentSchemaVersion, "1.0")
	}
}

func TestNewProducesLowercaseSHA256Hash(t *testing.T) {
	envelope, err := New("proposal", "proposal-1", "", map[string]string{"name": "Ada"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if len(envelope.ContentHash) != 64 {
		t.Fatalf("hash length = %d, want 64", len(envelope.ContentHash))
	}
	if envelope.ContentHash != strings.ToLower(envelope.ContentHash) {
		t.Fatalf("hash = %q, want lower-case hex", envelope.ContentHash)
	}
}

func TestNewChangesHashWhenContentChanges(t *testing.T) {
	first, err := New("proposal", "proposal-1", "", map[string]string{"name": "Ada"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	second, err := New("proposal", "proposal-1", "", map[string]string{"name": "Grace"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if first.ContentHash == second.ContentHash {
		t.Fatalf("different content produced the same hash %q", first.ContentHash)
	}
}

func TestEnvelopeOmitsEmptyParentIDFromJSON(t *testing.T) {
	envelope, err := New("proposal", "proposal-1", "", map[string]string{"name": "Ada"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), "parent_id") {
		t.Fatalf("JSON %s unexpectedly contains parent_id", encoded)
	}
}

func TestEnvelopeIncludesSetParentIDInJSON(t *testing.T) {
	envelope, err := New("proposal", "proposal-2", "proposal-1", map[string]string{"name": "Ada"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"parent_id":"proposal-1"`) {
		t.Fatalf("JSON %s does not contain parent_id", encoded)
	}
}

func TestNewReturnsMarshalErrorForUnsupportedContent(t *testing.T) {
	_, err := New("proposal", "proposal-1", "", func() {})
	if err == nil {
		t.Fatal("New() error = nil, want marshal error")
	}
}
