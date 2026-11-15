package council

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/aicouncil/aicouncil/internal/observability/metrics"
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
			requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
			if requestID == "" || len(requestID) > 128 {
				requestID = newRequestID()
			}
			w.Header().Set("X-Request-ID", requestID)
			start := time.Now()
			tracked := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(tracked, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, requestID)))
			if tracked.status == 0 {
				tracked.status = http.StatusOK
			}
			duration := time.Since(start)
			if m != nil {
				m.RecordHTTP(tracked.status, duration)
			}
			logger.Info("http_request", "request_id", requestID, "method", r.Method, "path", r.URL.Path, "status", tracked.status, "duration_ms", duration.Milliseconds())
		}
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *statusWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type requestIDKey struct{}

func RequestID(r *http.Request) string {
	value, _ := r.Context().Value(requestIDKey{}).(string)
	return value
}
func newRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(value[:])
}
