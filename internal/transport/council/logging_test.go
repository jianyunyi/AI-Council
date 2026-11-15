package council

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aicouncil/aicouncil/internal/observability/metrics"

	"github.com/stretchr/testify/require"
)

func TestRequestLoggerSetsAndPreservesRequestID(t *testing.T) {
	h := RequestLogger(slog.New(slog.NewTextHandler(httptest.NewRecorder(), nil)))(func(w http.ResponseWriter, r *http.Request) {
		require.NotEmpty(t, RequestID(r))
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.NotEmpty(t, rec.Header().Get("X-Request-ID"))
	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-ID", "trace-123")
	rec = httptest.NewRecorder()
	h(rec, req)
	require.Equal(t, "trace-123", rec.Header().Get("X-Request-ID"))
}

func TestRequestLoggerRecordsHTTPStatusAndLatencyMetrics(t *testing.T) {
	m := metrics.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := RequestLoggerWithMetrics(logger, m)(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	output := httptest.NewRecorder()
	m.Handler(output, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(output.Body.String(), `aicouncil_http_responses_total{status_class="5xx"} 1`) {
		t.Fatalf("HTTP 5xx metric missing: %s", output.Body.String())
	}
	if !strings.Contains(output.Body.String(), "aicouncil_http_request_duration_seconds_count 1") {
		t.Fatalf("HTTP duration metric missing: %s", output.Body.String())
	}
}
