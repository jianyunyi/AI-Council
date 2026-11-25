package group

import (
	"errors"
	"testing"
)

func TestApplyPolicyHumanApprovalLocked(t *testing.T) {
	_, err := applyPolicy(DefaultPolicy(), map[string]any{"require_human_approval": false})
	if !errors.Is(err, errHumanApproval) {
		t.Fatalf("want human_approval_required, got %v", err)
	}
	p, err := applyPolicy(DefaultPolicy(), map[string]any{"require_human_approval": true, "max_reviewers": float64(3)})
	if err != nil || !p.RequireHumanApproval || p.MaxReviewers != 3 {
		t.Fatalf("p=%+v err=%v", p, err)
	}
	if !decodePolicy([]byte(`{"require_human_approval":false}`)).RequireHumanApproval {
		t.Fatal("stored false must decode as true")
	}
}

func TestApplyPolicyValidation(t *testing.T) {
	bad := []map[string]any{
		{"unknown_key": 1},
		{"proposal_review_rule": "majority"},
		{"proposal_review_rule": "quorum"}, // quorum missing
		{"min_reviewers": float64(0)},
		{"max_reviewers": float64(9)},
		{"review_deadline_minutes": float64(1)},
		{"context_token_budget": float64(10)},
	}
	for _, patch := range bad {
		if _, err := applyPolicy(DefaultPolicy(), patch); err == nil {
			t.Fatalf("patch %v must fail", patch)
		}
	}
	q := 2
	p, err := applyPolicy(DefaultPolicy(), map[string]any{"proposal_review_rule": "quorum", "proposal_quorum": float64(q)})
	if err != nil || p.ProposalQuorum == nil || *p.ProposalQuorum != 2 {
		t.Fatalf("p=%+v err=%v", p, err)
	}
}

func TestValidateParamsOverride(t *testing.T) {
	if _, err := validateParamsOverride(map[string]any{"temperature": 3.0}); err == nil {
		t.Fatal("temperature 3 must fail")
	}
	if _, err := validateParamsOverride(map[string]any{"api_key": "x"}); err == nil {
		t.Fatal("unknown key must fail")
	}
	if b, err := validateParamsOverride(map[string]any{"temperature": 0.5}); err != nil || len(b) == 0 {
		t.Fatalf("b=%s err=%v", b, err)
	}
}
