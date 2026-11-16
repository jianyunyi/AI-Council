package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/aicouncil/aicouncil/internal/observability/metrics"
)

type metricsProvider struct{}

func (metricsProvider) Name() string { return "openai" }
func (metricsProvider) Generate(context.Context, Request) (Response, error) {
	return Response{Usage: Usage{InputTokens: 10, OutputTokens: 5}}, nil
}

func TestMeteredProviderRecordsCallsCostAndRetries(t *testing.T) {
	collector := metrics.New()
	p := WithCostMeter(metricsProvider{}, collector)
	ctx := WithRetryCallback(context.Background(), func() { collector.ProviderRetries.Add(1) })
	RecordRetry(ctx)
	response, err := p.Generate(ctx, Request{Model: "gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Usage.EstimatedCostMicros == 0 {
		t.Fatal("expected price-table cost estimate")
	}
	recorder := httptest.NewRecorder()
	collector.Handler(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, want := range []string{"aicouncil_provider_calls_total 1", "aicouncil_provider_retries_total 1", "aicouncil_provider_estimated_cost_micros_total " + strconv.FormatInt(response.Usage.EstimatedCostMicros, 10)} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Errorf("missing metric %q in output:\n%s", want, recorder.Body.String())
		}
	}
}
