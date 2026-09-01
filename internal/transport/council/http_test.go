package council

import (
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
