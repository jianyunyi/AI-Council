package council

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/aicouncil/aicouncil/internal/security/rbac"
)

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
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				next.ServeHTTP(w, r)
				return
			}
			if service == nil {
				http.Error(w, `{"error":{"code":"unauthorized","message":"rbac service unavailable"}}`, http.StatusUnauthorized)
				return
			}
			raw := strings.TrimSpace(r.Header.Get("Authorization"))
			if len(raw) < 7 || !strings.EqualFold(raw[:7], "bearer ") {
				writeAuthError(w, http.StatusUnauthorized, "valid bearer token required")
				return
			}
			err := service.Authorize(r.Context(), strings.TrimSpace(raw[7:]), role)
			if err == rbac.ErrForbidden {
				writeAuthError(w, http.StatusForbidden, "insufficient role")
				return
			}
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "invalid bearer token")
				return
			}
			next.ServeHTTP(w, r)
		}
	}
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"` + message + `"}}`))
}
