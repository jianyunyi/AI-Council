package council

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/aicouncil/aicouncil/internal/security/rbac"
	"github.com/aicouncil/aicouncil/internal/security/tlsconfig"
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

// NewTLSServerWithAPIAndAuth enables service-owned TLS with certificate
// rotation. The reloader is called by net/http for every new handshake.
func NewTLSServerWithAPIAndAuth(conf rest.RestConf, api *API, token, certFile, keyFile string) (*rest.Server, error) {
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("tls certificate and key are required")
	}
	reloader, err := tlsconfig.New(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	conf.CertFile, conf.KeyFile = certFile, keyFile
	conf.Middlewares.Log = true
	conf.Middlewares.Prometheus = true
	conf.Middlewares.Metrics = true
	conf.Middlewares.Recover = true
	server, err := rest.NewServer(conf, rest.WithTLSConfig(reloader.Config()))
	if err != nil {
		return nil, err
	}
	server.Use(RequestLoggerWithMetrics(slog.Default(), api.metrics))
	if token != "" {
		server.Use(BearerAuth(token))
	}
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/healthz", Handler: healthHandler})
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/metrics", Handler: api.metrics.Handler})
	for _, route := range api.Routes() {
		server.AddRoute(route)
	}
	return server, nil
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

func NewTLSServerWithAPIAndRBAC(conf rest.RestConf, api *API, service *rbac.Service, role, certFile, keyFile string) (*rest.Server, error) {
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("tls certificate and key are required")
	}
	reloader, err := tlsconfig.New(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	conf.CertFile, conf.KeyFile = certFile, keyFile
	conf.Middlewares.Log, conf.Middlewares.Prometheus, conf.Middlewares.Metrics, conf.Middlewares.Recover = true, true, true, true
	server, err := rest.NewServer(conf, rest.WithTLSConfig(reloader.Config()))
	if err != nil {
		return nil, err
	}
	server.Use(RequestLoggerWithMetrics(slog.Default(), api.metrics))
	server.Use(RBACAuth(service, role))
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/healthz", Handler: healthHandler})
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/metrics", Handler: api.metrics.Handler})
	for _, route := range api.Routes() {
		server.AddRoute(route)
	}
	return server, nil
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
