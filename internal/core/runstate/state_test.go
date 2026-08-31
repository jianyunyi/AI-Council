package runstate

import "testing"

func TestCanTransitionAllowsEveryLegalEdge(t *testing.T) {
	tests := []struct {
		name string
		from State
		to   State
	}{
		{"draft to analyzing", DRAFT, ANALYZING},
		{"draft to cancelled", DRAFT, CANCELLED},
		{"analyzing to proposing", ANALYZING, PROPOSING},
		{"analyzing to failed", ANALYZING, FAILED},
		{"analyzing to cancelled", ANALYZING, CANCELLED},
		{"proposing to reviewing", PROPOSING, REVIEWING},
		{"proposing to failed", PROPOSING, FAILED},
		{"proposing to cancelled", PROPOSING, CANCELLED},
		{"reviewing to judging", REVIEWING, JUDGING},
		{"reviewing to failed", REVIEWING, FAILED},
		{"reviewing to cancelled", REVIEWING, CANCELLED},
		{"judging to red team", JUDGING, RED_TEAM},
		{"judging to failed", JUDGING, FAILED},
		{"judging to cancelled", JUDGING, CANCELLED},
		{"red team to judging", RED_TEAM, JUDGING},
		{"red team to awaiting approval", RED_TEAM, AWAITING_APPROVAL},
		{"red team to failed", RED_TEAM, FAILED},
		{"red team to cancelled", RED_TEAM, CANCELLED},
		{"awaiting approval to executing", AWAITING_APPROVAL, EXECUTING},
		{"awaiting approval to cancelled", AWAITING_APPROVAL, CANCELLED},
		{"executing to verifying", EXECUTING, VERIFYING},
		{"executing to failed", EXECUTING, FAILED},
		{"executing to cancelled", EXECUTING, CANCELLED},
		{"verifying to succeeded", VERIFYING, SUCCEEDED},
		{"verifying to replanning", VERIFYING, REPLANNING},
		{"verifying to failed", VERIFYING, FAILED},
		{"verifying to cancelled", VERIFYING, CANCELLED},
		{"replanning to reviewing", REPLANNING, REVIEWING},
		{"replanning to failed", REPLANNING, FAILED},
		{"replanning to cancelled", REPLANNING, CANCELLED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !CanTransition(tt.from, tt.to) {
				t.Fatalf("CanTransition(%q, %q) = false, want true", tt.from, tt.to)
			}
		})
	}
}

func TestCanTransitionRejectsIllegalTransitions(t *testing.T) {
	tests := []struct {
		name string
		from State
		to   State
	}{
		{"draft skips to executing", DRAFT, EXECUTING},
		{"analyzing skips to judging", ANALYZING, JUDGING},
		{"proposing skips to red team", PROPOSING, RED_TEAM},
		{"reviewing skips to awaiting approval", REVIEWING, AWAITING_APPROVAL},
		{"judging skips to executing", JUDGING, EXECUTING},
		{"red team skips to executing", RED_TEAM, EXECUTING},
		{"awaiting approval skips to succeeded", AWAITING_APPROVAL, SUCCEEDED},
		{"executing skips to succeeded", EXECUTING, SUCCEEDED},
		{"verifying skips to analyzing", VERIFYING, ANALYZING},
		{"replanning skips to executing", REPLANNING, EXECUTING},
		{"succeeded to analyzing", SUCCEEDED, ANALYZING},
		{"failed to analyzing", FAILED, ANALYZING},
		{"cancelled to analyzing", CANCELLED, ANALYZING},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if CanTransition(tt.from, tt.to) {
				t.Fatalf("CanTransition(%q, %q) = true, want false", tt.from, tt.to)
			}
		})
	}
}
