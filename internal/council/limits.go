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

func (m Meter) Allows(next provider.Usage, l Limits) bool {
	return (l.MaxInputTokens <= 0 || m.InputTokens+next.InputTokens <= l.MaxInputTokens) && (l.MaxOutputTokens <= 0 || m.OutputTokens+next.OutputTokens <= l.MaxOutputTokens) && (l.MaxCostMicros <= 0 || m.CostMicros+next.EstimatedCostMicros <= l.MaxCostMicros)
}
