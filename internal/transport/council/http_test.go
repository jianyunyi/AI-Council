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
	if len(routes) != 1 {
		t.Fatalf("route count = %d, want 1", len(routes))
	}
	if routes[0].Method != http.MethodGet || routes[0].Path != "/healthz" {
		t.Fatalf("route = %s %s, want GET /healthz", routes[0].Method, routes[0].Path)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	routes[0].Handler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Body.String(); got != `{"status":"ok"}` {
		t.Fatalf("body = %q, want health JSON", got)
	}
}
