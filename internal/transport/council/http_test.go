package council

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zeromicro/go-zero/rest"
)

func TestHealthRoute(t *testing.T) {
	server := NewServer(rest.RestConf{Host: "127.0.0.1", Port: 18080})
	routes := server.Routes()
	var health rest.Route
	for _, route := range routes {
		if route.Method == http.MethodGet && route.Path == "/healthz" {
			health = route
			break
		}
	}
	if health.Path == "" {
		t.Fatalf("health route missing from %d routes", len(routes))
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	health.Handler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Body.String(); got != `{"status":"ok"}` {
		t.Fatalf("body = %q, want health JSON", got)
	}
}

func TestReadyRouteReportsDependencyState(t *testing.T) {
	tests := []struct {
		name       string
		check      func(context.Context) error
		wantStatus int
		wantBody   string
	}{
		{name: "ready", wantStatus: http.StatusOK, wantBody: `{"status":"ready"}`},
		{name: "dependency unavailable", check: func(context.Context) error { return errors.New("dependency unavailable") }, wantStatus: http.StatusServiceUnavailable, wantBody: `{"status":"not_ready"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := NewAPI()
			if test.check != nil {
				api.WithReadinessCheck(test.check)
			}
			server := NewServerWithAPI(rest.RestConf{Host: "127.0.0.1", Port: 18080}, api)
			var ready rest.Route
			for _, route := range server.Routes() {
				if route.Method == http.MethodGet && route.Path == "/readyz" {
					ready = route
					break
				}
			}
			if ready.Path == "" {
				t.Fatal("ready route is not registered")
			}
			recorder := httptest.NewRecorder()
			ready.Handler(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
			if recorder.Body.String() != test.wantBody {
				t.Fatalf("body = %q, want %q", recorder.Body.String(), test.wantBody)
			}
		})
	}
}
