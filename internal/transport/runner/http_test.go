package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthRoute(t *testing.T) {
	router := NewRouter()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Body.String(); got != `{"status":"ok"}` {
		t.Fatalf("body = %q, want health JSON", got)
	}
}

func TestShutdownRouteRequiresTokenAndCallsShutdown(t *testing.T) {
	called := make(chan struct{}, 1)
	router := NewRouterWithShutdown("local-session-token", func(context.Context) {
		called <- struct{}{}
	})

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/shutdown", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/shutdown", nil)
	request.Header.Set("Authorization", "Bearer local-session-token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("authorized status = %d, want 202", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "shutting_down") {
		t.Fatalf("body = %q, want shutdown response", recorder.Body.String())
	}
	select {
	case <-called:
	default:
		t.Fatal("authorized shutdown did not invoke callback")
	}
}
