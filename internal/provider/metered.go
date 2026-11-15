package provider

import (
	"context"

	"github.com/aicouncil/aicouncil/internal/observability/metrics"
)

// Metered wraps a provider and attaches the current price-table estimate to
// every response, allowing council limits and invocation persistence to use a
// consistent cost value regardless of vendor implementation.
type Metered struct {
	Inner   ModelProvider
	Metrics *metrics.Metrics
}

func (m Metered) Name() string { return m.Inner.Name() }

func (m Metered) Generate(ctx context.Context, req Request) (Response, error) {
	if m.Metrics != nil {
		ctx = WithRetryCallback(ctx, func() { m.Metrics.RecordProviderRetry(m.Inner.Name()) })
	}
	resp, err := m.Inner.Generate(ctx, req)
	if err == nil {
		resp.Usage.EstimatedCostMicros = EstimateCost(req.Model, resp.Usage)
	}
	if m.Metrics != nil {
		m.Metrics.RecordProviderCall(m.Inner.Name(), err == nil, 0, resp.Usage.EstimatedCostMicros)
	}
	return resp, err
}

func WithCostMeter(p ModelProvider, collectors ...*metrics.Metrics) ModelProvider {
	var collector *metrics.Metrics
	if len(collectors) > 0 {
		collector = collectors[0]
	}
	return Metered{Inner: p, Metrics: collector}
}
