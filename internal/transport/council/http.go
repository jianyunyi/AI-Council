package council

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	identitysvc "github.com/aicouncil/aicouncil/internal/security/identity"
	oauthsvc "github.com/aicouncil/aicouncil/internal/security/oauth"
	"github.com/aicouncil/aicouncil/internal/security/ratelimit"
	"github.com/aicouncil/aicouncil/internal/security/rbac"
	"github.com/aicouncil/aicouncil/internal/security/tlsconfig"
	"github.com/zeromicro/go-zero/rest"
)

func (a *API) rateLimitMiddleware() func(http.HandlerFunc) http.HandlerFunc {
	limiter := ratelimit.New(time.Minute, 60, 10000)
	return limiter.MiddlewareWithReject(func(r *http.Request) string { return requestOwnerID(r) }, ratelimit.LimitedAPIRoute, a.metrics.RecordRateLimited)
}

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
	server.Use(api.rateLimitMiddleware())
	if token != "" {
		server.Use(BearerAuthWithMetrics(token, api.metrics))
	}
	server.AddRoute(rest.Route{
		Method:  http.MethodGet,
		Path:    "/healthz",
		Handler: healthHandler,
	})
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/readyz", Handler: api.readyHandler})
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
	server.Use(api.rateLimitMiddleware())
	if token != "" {
		server.Use(BearerAuthWithMetrics(token, api.metrics))
	}
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/healthz", Handler: healthHandler})
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/readyz", Handler: api.readyHandler})
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/metrics", Handler: api.metrics.Handler})
	for _, route := range api.Routes() {
		server.AddRoute(route)
	}
	return server, nil
}

// NewServerWithAPIAndRBAC creates a REST server backed by the persistent
// user/role database instead of a single shared bearer token.
func NewServerWithAPIAndRBAC(conf rest.RestConf, api *API, service *rbac.Service, role string) *rest.Server {
	return NewServerWithAPIAndRBACWithOptions(conf, api, service, role, defaultSessionOptions())
}

// NewServerWithAPIAndRBACWithOptions adds password sessions to the persistent
// RBAC server while preserving the original constructor for existing callers.
func NewServerWithAPIAndRBACWithOptions(conf rest.RestConf, api *API, service *rbac.Service, role string, options SessionOptions) *rest.Server {
	return NewServerWithAPIAndRBACWithIdentity(conf, api, service, role, options, nil, nil)
}

func NewServerWithAPIAndRBACWithIdentity(conf rest.RestConf, api *API, service *rbac.Service, role string, options SessionOptions, identity *identitysvc.Service, mailer Mailer) *rest.Server {
	return NewServerWithAPIAndAuthFlows(conf, api, service, role, options, identity, mailer, nil)
}

func NewServerWithAPIAndAuthFlows(conf rest.RestConf, api *API, service *rbac.Service, role string, options SessionOptions, identity *identitysvc.Service, mailer Mailer, oauth *oauthsvc.Service) *rest.Server {
	// go-zero's LogHandler dumps request bodies and headers for 5xx responses.
	// RBAC endpoints carry passwords and session credentials, so retain only our
	// metadata-only request logger below.
	conf.Middlewares.Log = false
	conf.Middlewares.Prometheus = true
	conf.Middlewares.Metrics = true
	conf.Middlewares.Recover = true
	server := rest.MustNewServer(conf)
	server.Use(RequestLoggerWithMetrics(slog.Default(), api.metrics))
	server.Use(RBACAuthWithMetrics(service, role, options, api.metrics))
	server.Use(api.rateLimitMiddleware())
	registerRBACServerRoutes(server, api, NewRBACAPIWithAuthFlows(service, options, identity, mailer, oauth))
	return server
}

func NewTLSServerWithAPIAndRBAC(conf rest.RestConf, api *API, service *rbac.Service, role, certFile, keyFile string) (*rest.Server, error) {
	return NewTLSServerWithAPIAndRBACWithOptions(conf, api, service, role, certFile, keyFile, defaultSessionOptions())
}

// NewTLSServerWithAPIAndRBACWithOptions is the TLS equivalent of the options
// constructor and uses the same route registration as the HTTP server.
func NewTLSServerWithAPIAndRBACWithOptions(conf rest.RestConf, api *API, service *rbac.Service, role, certFile, keyFile string, options SessionOptions) (*rest.Server, error) {
	return NewTLSServerWithAPIAndRBACWithIdentity(conf, api, service, role, certFile, keyFile, options, nil, nil)
}

func NewTLSServerWithAPIAndRBACWithIdentity(conf rest.RestConf, api *API, service *rbac.Service, role, certFile, keyFile string, options SessionOptions, identity *identitysvc.Service, mailer Mailer) (*rest.Server, error) {
	return NewTLSServerWithAPIAndAuthFlows(conf, api, service, role, certFile, keyFile, options, identity, mailer, nil)
}

func NewTLSServerWithAPIAndAuthFlows(conf rest.RestConf, api *API, service *rbac.Service, role, certFile, keyFile string, options SessionOptions, identity *identitysvc.Service, mailer Mailer, oauth *oauthsvc.Service) (*rest.Server, error) {
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("tls certificate and key are required")
	}
	reloader, err := tlsconfig.New(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	conf.CertFile, conf.KeyFile = certFile, keyFile
	conf.Middlewares.Log, conf.Middlewares.Prometheus, conf.Middlewares.Metrics, conf.Middlewares.Recover = false, true, true, true
	server, err := rest.NewServer(conf, rest.WithTLSConfig(reloader.Config()))
	if err != nil {
		return nil, err
	}
	server.Use(RequestLoggerWithMetrics(slog.Default(), api.metrics))
	server.Use(RBACAuthWithMetrics(service, role, options, api.metrics))
	server.Use(api.rateLimitMiddleware())
	registerRBACServerRoutes(server, api, NewRBACAPIWithAuthFlows(service, options, identity, mailer, oauth))
	return server, nil
}

func registerRBACServerRoutes(server *rest.Server, api *API, rbacAPI *RBACAPI) {
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/healthz", Handler: healthHandler})
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/readyz", Handler: api.readyHandler})
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/metrics", Handler: api.metrics.Handler})
	for _, route := range api.Routes() {
		server.AddRoute(route)
	}
	for _, route := range api.ProviderKeyRoutes() {
		server.AddRoute(route)
	}
	for _, route := range api.WorkspaceShareRoutes() {
		server.AddRoute(route)
	}
	for _, route := range rbacAPI.Routes() {
		server.AddRoute(route)
	}
}

func (a *API) readyHandler(w http.ResponseWriter, r *http.Request) {
	for _, check := range a.readinessChecks {
		if err := check(r.Context()); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"not_ready"}`))
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ready"}`))
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
