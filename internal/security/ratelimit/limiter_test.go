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
func TestClientIPHonorsTrustedProxyXFF(t *testing.T) {
	// Direct peer is an internal proxy -> trust XFF last hop.
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	r.RemoteAddr = "172.18.0.5:5000" // compose app network
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := clientIP(r); got != "ip:203.0.113.9" {
		t.Fatalf("trusted XFF ip = %q, want ip:203.0.113.9", got)
	}
	// Untrusted direct peer -> ignore spoofed XFF.
	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	r2.RemoteAddr = "203.0.113.9:5000"
	r2.Header.Set("X-Forwarded-For", "10.0.0.1")
	if got := clientIP(r2); got != "ip:203.0.113.9" {
		t.Fatalf("untrusted XFF should be ignored, got %q", got)
	}
}

func TestLoginBucketsPerClientIPNotGlobal(t *testing.T) {
	l := New(time.Minute, 1, 100)
	h := l.MiddlewareWithReject(func(*http.Request) string { return "" }, LimitedAPIRoute, func() {})(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	call := func(peer string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		req.RemoteAddr = "172.18.0.5:5000"
		req.Header.Set("X-Forwarded-For", peer)
		h(rec, req)
		return rec.Code
	}
	if call("203.0.113.1") != http.StatusNoContent {
		t.Fatal("first client first attempt should pass")
	}
	if call("203.0.113.1") != http.StatusTooManyRequests {
		t.Fatal("same client second attempt should be limited")
	}
	if call("203.0.113.2") != http.StatusNoContent {
		t.Fatal("a different client must not share the first client's bucket")
	}
}
