package council

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func BearerAuth(expected string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
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
