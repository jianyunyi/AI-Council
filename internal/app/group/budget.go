package group

import (
	"context"
	"fmt"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// Budget pre-flight and pricing (P6 §2 of the plan).
//
// Two independent caps from the group Budget:
//   - TokenCap: total input+output tokens across ALL invocations of the
//     group. Always enforced (tokens are always reported by providers).
//   - USDCap: total estimated USD across the group, and
//     PerRequirementUSDCap per requirement. Enforced only when the pricing
//     table knows the model; unknown models are charged 0 and therefore
//     only token-capped (documented v1 limitation, WIRING W11).
//
// The check runs BEFORE every provider call with a conservative estimate
// of the upcoming call, so a group at its cap fails with
// budget_exhausted and zero additional spend.

// priceFor returns (inputUSDPer1M, outputUSDPer1M, known). W11: the table
// starts empty — no USD pricing is configured in v1, so only token caps
// bind. Fill entries as providers/models are priced; unknown models stay
// token-cap-only by design.
func priceFor(model string) (float64, float64, bool) {
	return 0, 0, false
}

// estimateCallCost estimates the USD cost of one call with the given
// prompt size and a worst-case output size. Unknown model -> (0, false).
func estimateCallCost(model string, estInputTokens, maxOutputTokens int64) (float64, bool) {
	inPrice, outPrice, known := priceFor(model)
	if !known {
		return 0, false
	}
	return float64(estInputTokens)/1e6*inPrice + float64(maxOutputTokens)/1e6*outPrice, true
}

// checkBudget verifies the group (and optionally one requirement) can
// afford another estimated call. estInputTokens is the caller's prompt
// estimate; the output allowance is a flat worst case.
func (s *Service) checkBudget(ctx context.Context, g *sqlite.ChatGroupRecord, requirementID, model string, estInputTokens int64) error {
	budget := decodeBudget(g.Budget)
	const maxOutputTokens = 8192

	groupSpend, err := s.reqRepo.GroupSpend(ctx, g.ID, "")
	if err != nil {
		return err
	}
	estCost, priced := estimateCallCost(model, estInputTokens, maxOutputTokens)

	if budget.TokenCap > 0 {
		used := groupSpend.InputTokens + groupSpend.OutputTokens
		if used+estInputTokens+maxOutputTokens > budget.TokenCap {
			return fmt.Errorf("%s: group token budget exhausted (%d of %d used)", FailBudgetExhausted, used, budget.TokenCap)
		}
	}
	if priced && budget.USDCap > 0 {
		usedUSD := float64(groupSpend.CostMicros) / 1e6
		if usedUSD+estCost > budget.USDCap {
			return fmt.Errorf("%s: group USD budget exhausted ($%.2f of $%.2f used)", FailBudgetExhausted, usedUSD, budget.USDCap)
		}
	}
	if priced && budget.PerRequirementUSDCap > 0 && requirementID != "" {
		reqSpend, err := s.reqRepo.GroupSpend(ctx, g.ID, requirementID)
		if err != nil {
			return err
		}
		usedUSD := float64(reqSpend.CostMicros) / 1e6
		if usedUSD+estCost > budget.PerRequirementUSDCap {
			return fmt.Errorf("%s: per-requirement USD budget exhausted ($%.2f of $%.2f used)", FailBudgetExhausted, usedUSD, budget.PerRequirementUSDCap)
		}
	}
	return nil
}

// costMicros converts a finished call's usage into estimated USD micros
// (0 for unpriced models).
func costMicros(model string, inputTokens, outputTokens int64) int64 {
	inPrice, outPrice, known := priceFor(model)
	if !known {
		return 0
	}
	usd := float64(inputTokens)/1e6*inPrice + float64(outputTokens)/1e6*outPrice
	return int64(usd * 1e6)
}
