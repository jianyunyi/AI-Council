package council

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/aicouncil/aicouncil/internal/security/rbac"
)

// SessionOptions configures the password-login cookie. A zero value uses safe
// production defaults; callers can explicitly disable Secure for local HTTP
// development by supplying any non-zero option set with CookieSecure false.
type SessionOptions struct {
	CookieName   string
	CookieSecure bool
	TTL          time.Duration
}

func defaultSessionOptions() SessionOptions {
	return SessionOptions{CookieName: "aicouncil_session", CookieSecure: true, TTL: 8 * time.Hour}
}

func normalizeSessionOptions(options SessionOptions) SessionOptions {
	if options == (SessionOptions{}) {
		return defaultSessionOptions()
	}
	defaults := defaultSessionOptions()
	if strings.TrimSpace(options.CookieName) == "" {
		options.CookieName = defaults.CookieName
	}
	if options.TTL <= 0 {
		options.TTL = defaults.TTL
	}
	return options
}

type identityContextKey struct{}

// IdentityFromRequest returns the identity authenticated by RBACAuth.
func IdentityFromRequest(r *http.Request) (rbac.Identity, bool) {
	identity, ok := r.Context().Value(identityContextKey{}).(rbac.Identity)
	return identity, ok
}

func BearerAuth(expected string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				next.ServeHTTP(w, r)
				return
			}
			if expected == "" {
				next.ServeHTTP(w, r)
				return
			}
			raw := strings.TrimSpace(r.Header.Get("Authorization"))
			if len(raw) < 7 || !strings.EqualFold(raw[:7], "bearer ") || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(raw[7:])), []byte(expected)) != 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"valid bearer token required"}}`))
				return
			}
			next.ServeHTTP(w, r)
		}
	}
}

// RBACAuth validates a bearer token against the persistent user/role store.
// It can be layered on the REST server in place of the static token middleware.
func RBACAuth(service *rbac.Service, role string) func(http.HandlerFunc) http.HandlerFunc {
	return RBACAuthWithOptions(service, role, defaultSessionOptions())
}

// RBACAuthWithOptions authenticates first with a bearer token (for desktop and
// CLI clients), then with the password-login session cookie. The retained role
// argument is intentionally ignored: route permissions supersede the old
// single-role gate while keeping source compatibility with existing callers.
func RBACAuthWithOptions(service *rbac.Service, _ string, options SessionOptions) func(http.HandlerFunc) http.HandlerFunc {
	options = normalizeSessionOptions(options)
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if isPublicRBACPath(r.Method, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			if service == nil {
				writeRBACError(w, http.StatusUnauthorized, "unauthorized", "rbac service unavailable")
				return
			}
			identity, bearerPresented, err := authenticateRBACRequest(r, service, options)
			if err != nil {
				if err == rbac.ErrForbidden {
					writeRBACError(w, http.StatusForbidden, "forbidden", "permission denied")
					return
				}
				message := "valid bearer token or session required"
				if bearerPresented {
					message = "invalid bearer token"
				}
				writeRBACError(w, http.StatusUnauthorized, "unauthorized", message)
				return
			}
			permission, mapped := rbacRoutePermission(r.Method, r.URL.Path)
			if !mapped {
				writeRBACError(w, http.StatusForbidden, "forbidden", "permission denied")
				return
			}
			if permission != "" && !identityHasPermission(identity, permission) {
				writeRBACError(w, http.StatusForbidden, "forbidden", "permission denied")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityContextKey{}, identity)))
		}
	}
}

func authenticateRBACRequest(r *http.Request, service *rbac.Service, options SessionOptions) (rbac.Identity, bool, error) {
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(raw) >= 7 && strings.EqualFold(raw[:7], "bearer ") {
		identity, err := service.Authenticate(r.Context(), strings.TrimSpace(raw[7:]))
		if err == nil {
			return identity, true, nil
		}
		if err == rbac.ErrForbidden {
			return rbac.Identity{}, true, err
		}
	}
	cookie, err := r.Cookie(options.CookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return rbac.Identity{}, raw != "", rbac.ErrUnauthorized
	}
	identity, err := service.Authenticate(r.Context(), cookie.Value)
	return identity, raw != "", err
}

func identityHasPermission(identity rbac.Identity, permission string) bool {
	for _, granted := range identity.Permissions {
		if granted == permission || (strings.HasSuffix(granted, ":*") && strings.HasPrefix(permission, strings.TrimSuffix(granted, "*"))) {
			return true
		}
	}
	return false
}

func isPublicRBACPath(method, path string) bool {
	return path == "/healthz" || path == "/metrics" || (method == http.MethodPost && (path == "/api/v1/auth/login" || path == "/api/v1/auth/logout"))
}

func rbacRoutePermission(method, path string) (string, bool) {
	switch {
	case method == http.MethodGet && path == "/api/v1/auth/me":
		return "", true
	case method == http.MethodGet && path == "/api/v1/workspaces":
		return "workspace:read", true
	case method == http.MethodPost && path == "/api/v1/workspaces":
		return "workspace:write", true
	case method == http.MethodPost && path == "/api/v1/providers/test":
		return "workspace:write", true
	case method == http.MethodPost && path == "/api/v1/tasks":
		return "task:write", true
	case method == http.MethodGet && path == "/api/v1/admin/permissions":
		return "admin:permissions", true
	}
	if permission, ok := taskRoutePermission(method, path); ok {
		return permission, true
	}
	if (path == "/api/v1/admin/users" && (method == http.MethodGet || method == http.MethodPost)) || (singleSegment(path, "/api/v1/admin/users") && method == http.MethodPatch) {
		return "admin:users", true
	}
	if (path == "/api/v1/admin/roles" && (method == http.MethodGet || method == http.MethodPost)) || (singleSegment(path, "/api/v1/admin/roles") && method == http.MethodPatch) {
		return "admin:roles", true
	}
	return "", false
}

func taskRoutePermission(method, path string) (string, bool) {
	const prefix = "/api/v1/tasks/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	for _, part := range parts {
		if part == "" {
			return "", false
		}
	}
	if len(parts) == 1 && method == http.MethodGet {
		return "task:read", true
	}
	if len(parts) == 2 {
		if method == http.MethodGet && parts[1] == "events" {
			return "task:read", true
		}
		if method == http.MethodPost {
			switch parts[1] {
			case "start", "reject", "cancel":
				return "task:write", true
			case "approve":
				return "task:approve", true
			case "execute":
				return "task:execute", true
			}
		}
	}
	if len(parts) == 3 && method == http.MethodGet && parts[1] == "artifacts" {
		return "task:read", true
	}
	return "", false
}

func singleSegment(path, base string) bool {
	if !strings.HasPrefix(path, base+"/") {
		return false
	}
	segment := strings.TrimPrefix(path, base+"/")
	return segment != "" && !strings.Contains(segment, "/")
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	writeRBACError(w, status, "unauthorized", message)
}

func writeRBACError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + message + `"}}`))
}
