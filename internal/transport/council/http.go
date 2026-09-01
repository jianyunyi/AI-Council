package council

import (
	"log/slog"
	"net/http"

	"github.com/aicouncil/aicouncil/internal/security/rbac"
	"github.com/zeromicro/go-zero/rest"
)

func NewServer(conf rest.RestConf) *rest.Server {
	return NewServerWithAPI(conf, NewAPI())
}

func NewServerWithAPI(conf rest.RestConf, api *API) *rest.Server {
	return NewServerWithAPIAndAuth(conf, api, "")
}
func NewServerWithAPIAndAuth(conf rest.RestConf, api *API, token string) *rest.Server {
	conf.Middlewares.Log = true
	conf.Middlewares.Prometheus = true
	conf.Middlewares.Metrics = true
	conf.Middlewares.Recover = true
	server := rest.MustNewServer(conf)
	server.Use(RequestLoggerWithMetrics(slog.Default(), api.metrics))
	if token != "" {
		server.Use(BearerAuth(token))
	}
	server.AddRoute(rest.Route{
		Method:  http.MethodGet,
		Path:    "/healthz",
		Handler: healthHandler,
	})
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/metrics", Handler: api.metrics.Handler})
	for _, route := range api.Routes() {
		server.AddRoute(route)
	}
	return server
}

// NewServerWithAPIAndRBAC creates a REST server backed by the persistent
// user/role database instead of a single shared bearer token.
func NewServerWithAPIAndRBAC(conf rest.RestConf, api *API, service *rbac.Service, role string) *rest.Server {
	conf.Middlewares.Log = true
	conf.Middlewares.Prometheus = true
	conf.Middlewares.Metrics = true
	conf.Middlewares.Recover = true
	server := rest.MustNewServer(conf)
	server.Use(RequestLoggerWithMetrics(slog.Default(), api.metrics))
	server.Use(RBACAuth(service, role))
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/healthz", Handler: healthHandler})
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/metrics", Handler: api.metrics.Handler})
	for _, route := range api.Routes() {
		server.AddRoute(route)
	}
	return server
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
