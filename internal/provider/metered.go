package provider

import "context"

// Metered wraps a provider and attaches the current price-table estimate to
// every response, allowing council limits and invocation persistence to use a
// consistent cost value regardless of vendor implementation.
type Metered struct{ Inner ModelProvider }

func (m Metered) Name() string { return m.Inner.Name() }

func (m Metered) Generate(ctx context.Context, req Request) (Response, error) {
	resp, err := m.Inner.Generate(ctx, req)
	if err == nil {
		resp.Usage.EstimatedCostMicros = EstimateCost(req.Model, resp.Usage)
	}
	return resp, err
}

func WithCostMeter(p ModelProvider) ModelProvider { return Metered{Inner: p} }
