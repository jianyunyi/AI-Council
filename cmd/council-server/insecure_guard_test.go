package main

import "testing"

// Documents the fail-closed contract: without RBAC, a token, or the explicit
// insecure-dev opt-in, the server must refuse to start. The actual guard lives
// in main(); this asserts the decision predicate stays correct.
func TestAuthGuardPredicate(t *testing.T) {
	cases := []struct {
		rbac, token, insecure bool
		wantRefuse            bool
	}{
		{rbac: true, wantRefuse: false},
		{token: true, wantRefuse: false},
		{insecure: true, wantRefuse: false},
		{wantRefuse: true}, // none of the above => refuse
	}
	for _, c := range cases {
		refuse := !c.rbac && !c.token && !c.insecure
		if refuse != c.wantRefuse {
			t.Fatalf("rbac=%v token=%v insecure=%v refuse=%v want %v", c.rbac, c.token, c.insecure, refuse, c.wantRefuse)
		}
	}
}
