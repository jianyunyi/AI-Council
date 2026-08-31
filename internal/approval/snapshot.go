package approval

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/aicouncil/aicouncil/internal/council/schema"
)

var ErrHashMismatch = errors.New("approval hash mismatch")

type payload struct {
	RunID       string               `json:"run_id"`
	WorkspaceID string               `json:"workspace_id"`
	Plan        schema.ExecutionPlan `json:"plan"`
}

func Hash(runID, workspaceID string, plan schema.ExecutionPlan) (string, error) {
	raw, err := json.Marshal(payload{RunID: runID, WorkspaceID: workspaceID, Plan: plan})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func Verify(want, runID, workspaceID string, plan schema.ExecutionPlan) error {
	got, err := Hash(runID, workspaceID, plan)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		return ErrHashMismatch
	}
	return nil
}
