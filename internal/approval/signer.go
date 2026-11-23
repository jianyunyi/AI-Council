package approval

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/aicouncil/aicouncil/internal/council/schema"
)

var (
	ErrSignatureMismatch = errors.New("approval signature mismatch")
	ErrSignatureExpired  = errors.New("approval signature expired")
	ErrNoSigningKey      = errors.New("approval signing key is not configured")
)

// Signer produces and verifies keyed approval signatures. The key is shared
// between Council (issuer) and Runner (verifier) out of band via environment.
type Signer struct{ key []byte }

func NewSigner(key []byte) (*Signer, error) {
	if len(key) != 32 {
		return nil, ErrNoSigningKey
	}
	return &Signer{key: append([]byte(nil), key...)}, nil
}

// canonicalPlan mirrors the old Hash() normalization so the signed projection is
// exactly the executable plan the Runner will run (recovery is operator-only).
func canonicalPlan(plan schema.ExecutionPlan) ([]byte, error) {
	if plan.Acceptance == nil {
		plan.Acceptance = []string{}
	}
	if plan.Patches == nil {
		plan.Patches = []schema.Patch{}
	}
	if plan.Commands == nil {
		plan.Commands = []schema.Command{}
	}
	if plan.VerificationCommands == nil {
		plan.VerificationCommands = []schema.Command{}
	}
	plan.Recovery = nil
	return json.Marshal(plan)
}

func (s *Signer) payload(runID, workspaceID string, planVersion int, plan schema.ExecutionPlan, expiresAt int64, nonce string) ([]byte, error) {
	rawPlan, err := canonicalPlan(plan)
	if err != nil {
		return nil, err
	}
	// Length-prefixed concatenation avoids field-boundary ambiguity.
	var buf []byte
	add := func(b []byte) {
		buf = append(buf, []byte(strconv.Itoa(len(b)))...)
		buf = append(buf, ':')
		buf = append(buf, b...)
	}
	add([]byte(runID))
	add([]byte(workspaceID))
	add([]byte(strconv.Itoa(planVersion)))
	add(rawPlan)
	add([]byte(strconv.FormatInt(expiresAt, 10)))
	add([]byte(nonce))
	return buf, nil
}

func (s *Signer) Sign(runID, workspaceID string, planVersion int, plan schema.ExecutionPlan, expiresAt int64, nonce string) (string, error) {
	payload, err := s.payload(runID, workspaceID, planVersion, plan, expiresAt, nonce)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func (s *Signer) Verify(signature, runID, workspaceID string, planVersion int, plan schema.ExecutionPlan, expiresAt int64, nonce string) error {
	want, err := s.Sign(runID, workspaceID, planVersion, plan, expiresAt, nonce)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(signature), []byte(want)) != 1 {
		return ErrSignatureMismatch
	}
	return nil
}
