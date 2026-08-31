package council

import (
	"github.com/aicouncil/aicouncil/internal/provider"
	"time"
)

type Limits struct {
	Quorum          int
	MaxRounds       int
	MaxInputTokens  int64
	MaxOutputTokens int64
	MaxCostMicros   int64
	Timeout         time.Duration
}

type Meter struct{ InputTokens, OutputTokens, CostMicros int64 }

func QuorumMet(successes int, l Limits) bool {
	if l.Quorum <= 0 {
		return successes > 0
	}
	return successes >= l.Quorum
}

func (m Meter) Allows(next provider.Usage, l Limits) bool {
	return (l.MaxInputTokens <= 0 || m.InputTokens+next.InputTokens <= l.MaxInputTokens) && (l.MaxOutputTokens <= 0 || m.OutputTokens+next.OutputTokens <= l.MaxOutputTokens) && (l.MaxCostMicros <= 0 || m.CostMicros+next.EstimatedCostMicros <= l.MaxCostMicros)
}
