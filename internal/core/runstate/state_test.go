package runstate

import "testing"

func TestCanTransitionAllowsEveryLegalEdge(t *testing.T) {
	tests := []struct {
		name string
		from State
		to   State
	}{
		{"draft to analyzing", Draft, Analyzing},
		{"draft to cancelled", Draft, Cancelled},
		{"analyzing to proposing", Analyzing, Proposing},
		{"analyzing to failed", Analyzing, Failed},
		{"analyzing to cancelled", Analyzing, Cancelled},
		{"proposing to reviewing", Proposing, Reviewing},
		{"proposing to failed", Proposing, Failed},
		{"proposing to cancelled", Proposing, Cancelled},
		{"reviewing to judging", Reviewing, Judging},
		{"reviewing to failed", Reviewing, Failed},
		{"reviewing to cancelled", Reviewing, Cancelled},
		{"judging to red team", Judging, RedTeam},
		{"judging to failed", Judging, Failed},
		{"judging to cancelled", Judging, Cancelled},
		{"red team to judging", RedTeam, Judging},
		{"red team to awaiting approval", RedTeam, AwaitingApproval},
		{"red team to failed", RedTeam, Failed},
		{"red team to cancelled", RedTeam, Cancelled},
		{"awaiting approval to executing", AwaitingApproval, Executing},
		{"awaiting approval to cancelled", AwaitingApproval, Cancelled},
		{"executing to verifying", Executing, Verifying},
		{"executing to failed", Executing, Failed},
		{"executing to cancelled", Executing, Cancelled},
		{"verifying to succeeded", Verifying, Succeeded},
		{"verifying to replanning", Verifying, Replanning},
		{"verifying to failed", Verifying, Failed},
		{"verifying to cancelled", Verifying, Cancelled},
		{"replanning to reviewing", Replanning, Reviewing},
		{"replanning to failed", Replanning, Failed},
		{"replanning to cancelled", Replanning, Cancelled},
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
		{"draft skips to executing", Draft, Executing},
		{"analyzing skips to judging", Analyzing, Judging},
		{"proposing skips to red team", Proposing, RedTeam},
		{"reviewing skips to awaiting approval", Reviewing, AwaitingApproval},
		{"judging skips to executing", Judging, Executing},
		{"red team skips to executing", RedTeam, Executing},
		{"awaiting approval skips to succeeded", AwaitingApproval, Succeeded},
		{"executing skips to succeeded", Executing, Succeeded},
		{"verifying skips to analyzing", Verifying, Analyzing},
		{"replanning skips to executing", Replanning, Executing},
		{"succeeded to analyzing", Succeeded, Analyzing},
		{"failed to analyzing", Failed, Analyzing},
		{"cancelled to analyzing", Cancelled, Analyzing},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if CanTransition(tt.from, tt.to) {
				t.Fatalf("CanTransition(%q, %q) = true, want false", tt.from, tt.to)
			}
		})
	}
}
