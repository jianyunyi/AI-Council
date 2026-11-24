package group

import (
	"testing"
)

func TestExecOutcomeAndCaps(t *testing.T) {
	cases := []struct {
		status, code, want string
	}{
		{PDSucceeded, "", ExecSucceeded},
		{PDCancelled, "", ExecCancelled},
		{PDFailed, "cancelled", ExecCancelled},
		{PDFailed, "plan_changed", ExecNotExecuted},
		{PDFailed, "pushdown_refused_workspace", ExecNotExecuted},
		{PDFailed, "task_failed", ExecFailed},
		{PDFailed, "", ExecFailed},
	}
	for _, c := range cases {
		if got := execOutcomeOf(c.status, c.code); got != c.want {
			t.Errorf("execOutcomeOf(%q,%q) = %q, want %q", c.status, c.code, got, c.want)
		}
	}
	if maxCycles(Policy{}) != 2 {
		t.Error("default max cycles must be 2")
	}
	if maxCycles(Policy{MaxResultRounds: 3}) != 3 {
		t.Error("policy max cycles must win")
	}
	if maxAttemptsPerCycle(Policy{}) != 3 {
		t.Error("default attempts per cycle must be 3")
	}
	// C1: execution_failed is NOT terminal; the terminal set is
	// exactly cancelled/failed/proposal_failed/rejected/completed.
	if isTerminal(ReqExecutionFailed) || isTerminal(ReqExecuted) || isTerminal(ReqResultSubmitted) {
		t.Error("execution states must not be terminal")
	}
	for _, s := range []string{ReqCancelled, ReqFailed, ReqProposalFailed, ReqRejected, ReqCompleted} {
		if !isTerminal(s) {
			t.Errorf("%s must be terminal", s)
		}
	}
}
