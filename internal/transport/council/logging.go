package council

import (
	"github.com/aicouncil/aicouncil/internal/observability/metrics"
	"log/slog"
	"net/http"
	"time"
)

func RequestLogger(logger *slog.Logger) func(http.HandlerFunc) http.HandlerFunc {
	return requestLogger(logger, nil)
}

func RequestLoggerWithMetrics(logger *slog.Logger, m *metrics.Metrics) func(http.HandlerFunc) http.HandlerFunc {
	return requestLogger(logger, m)
}

func requestLogger(logger *slog.Logger, m *metrics.Metrics) func(http.HandlerFunc) http.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			next.ServeHTTP(w, r)
			if m != nil {
				m.Requests.Add(1)
			}
			logger.Info("http_request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds())
		}
	}
}
