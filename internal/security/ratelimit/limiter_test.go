package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aicouncil/aicouncil/internal/observability/metrics"
)

func TestAllowEnforcesLimitAndResetsWindow(t *testing.T) {
	l := New(20*time.Millisecond, 2, 4)
	if !l.Allow("ip:a") || !l.Allow("ip:a") {
		t.Fatal("first two requests should pass")
	}
	if l.Allow("ip:a") {
		t.Fatal("request over limit passed")
	}
	time.Sleep(25 * time.Millisecond)
	if !l.Allow("ip:a") {
		t.Fatal("bucket did not reset")
	}
}

func TestMiddlewareLimitsSelectedRoutesAndUsesKey(t *testing.T) {
	l := New(time.Minute, 1, 4)
	m := metrics.New()
	h := l.MiddlewareWithReject(func(*http.Request) string { return "user-1" }, LimitedAPIRoute, m.RecordRateLimited)(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	call := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "192.0.2.1:1000"
		h(rec, req)
		return rec
	}
	if got := call(http.MethodPost, "/api/v1/tasks").Code; got != http.StatusNoContent {
		t.Fatalf("first status=%d", got)
	}
	if got := call(http.MethodPost, "/api/v1/tasks").Code; got != http.StatusTooManyRequests {
		t.Fatalf("limited status=%d", got)
	}
	if got := call(http.MethodGet, "/api/v1/tasks").Code; got != http.StatusNoContent {
		t.Fatalf("unlimited route status=%d", got)
	}
	if got := m.RateLimited.Load(); got != 1 {
		t.Fatalf("rate limited metric=%d, want 1", got)
	}
}

func TestMaxKeysBoundsStorage(t *testing.T) {
	l := New(time.Minute, 1, 1)
	if !l.Allow("a") {
		t.Fatal("first key rejected")
	}
	if l.Allow("b") {
		t.Fatal("key overflow was admitted")
	}
}
