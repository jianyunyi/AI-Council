package group

import (
	"context"
	"strings"
	"testing"
	"time"

	store "github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// Minimal budget tests (the Quick draft's budget_test.go referenced a
// spendRepo helper and a nil reqRepo and could not compile; this version
// exercises checkBudget against the real ledger).

func TestCheckBudgetTokenCap(t *testing.T) {
	e := newP6Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, map[string]any{"token_cap": 1000})
	rec, err := e.repo.GetGroup(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	// No spend yet, but the worst-case output allowance (8192) alone
	// exceeds a 1000-token cap: the pre-flight must refuse.
	if err := e.svc.checkBudget(ctx, rec, "", "claude-x-1", 10); err == nil ||
		!strings.HasPrefix(err.Error(), FailBudgetExhausted) {
		t.Fatalf("checkBudget = %v, want budget_exhausted", err)
	}

	// A roomy cap passes with no spend and a small estimate.
	g2 := e.mkGroup(t, map[string]any{"token_cap": 100000})
	rec2, err := e.repo.GetGroup(ctx, g2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.checkBudget(ctx, rec2, "", "claude-x-1", 100); err != nil {
		t.Fatalf("checkBudget = %v, want nil", err)
	}
	// Record 95k tokens of spend; the next call no longer fits.
	inv := &store.GroupModelInvocationRecord{
		ID: "inv_test_1", GroupID: g2.ID, AIModelID: "aim_1", Model: "claude-x-1",
		Purpose: PurposePropose, Status: "ok", InputTokens: 90000, OutputTokens: 5000,
		CreatedAt: time.Now().UTC(),
	}
	if err := e.reqs.CreateInvocation(ctx, inv); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.checkBudget(ctx, rec2, "", "claude-x-1", 100); err == nil ||
		!strings.HasPrefix(err.Error(), FailBudgetExhausted) {
		t.Fatalf("checkBudget after spend = %v, want budget_exhausted", err)
	}
}

func TestCheckBudgetUSDUnenforcedForUnpricedModel(t *testing.T) {
	e := newP6Env(t)
	ctx := context.Background()
	// W11: the v1 pricing table is empty, so a USD cap cannot bind an
	// unpriced model — only the token cap can. This test pins that
	// documented behavior so filling the table later is a visible change.
	g := e.mkGroup(t, map[string]any{"usd_cap": 0.01})
	rec, err := e.repo.GetGroup(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.checkBudget(ctx, rec, "", "claude-x-1", 100); err != nil {
		t.Fatalf("checkBudget = %v, want nil for unpriced model", err)
	}
	if _, known := estimateCallCost("claude-x-1", 1000, 1000); known {
		t.Fatal("claude-x-1 unexpectedly priced in the v1 table")
	}
	if got := costMicros("claude-x-1", 1000, 1000); got != 0 {
		t.Fatalf("costMicros = %d, want 0 for unpriced model", got)
	}
}
