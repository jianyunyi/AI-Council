package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const CurrentSchemaVersion = "1.0"

type Envelope struct {
	SchemaVersion string          `json:"schema_version"`
	Type          string          `json:"type"`
	ID            string          `json:"id"`
	ParentID      string          `json:"parent_id,omitempty"`
	Content       json.RawMessage `json:"content"`
	ContentHash   string          `json:"content_hash"`
}

func New(kind, id, parentID string, content any) (Envelope, error) {
	raw, err := json.Marshal(content)
	if err != nil {
		return Envelope{}, err
	}

	sum := sha256.Sum256(raw)
	return Envelope{
		SchemaVersion: CurrentSchemaVersion,
		Type:          kind,
		ID:            id,
		ParentID:      parentID,
		Content:       raw,
		ContentHash:   hex.EncodeToString(sum[:]),
	}, nil
}
