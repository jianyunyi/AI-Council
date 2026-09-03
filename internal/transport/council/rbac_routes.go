package council

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aicouncil/aicouncil/internal/security/rbac"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/pathvar"
	"gorm.io/gorm"
)

// RBACAPI owns password session and administrative RBAC endpoints.
type RBACAPI struct {
	service  *rbac.Service
	options  SessionOptions
	mu       sync.Mutex
	failures map[string][]time.Time
	now      func() time.Time
}

func NewRBACAPI(service *rbac.Service, options SessionOptions) *RBACAPI {
	return &RBACAPI{service: service, options: normalizeSessionOptions(options), failures: make(map[string][]time.Time), now: time.Now}
}

func (a *RBACAPI) Routes() []rest.Route {
	return []rest.Route{
		{Method: http.MethodPost, Path: "/api/v1/auth/login", Handler: a.login},
		{Method: http.MethodGet, Path: "/api/v1/auth/me", Handler: a.me},
		{Method: http.MethodPost, Path: "/api/v1/auth/logout", Handler: a.logout},
		{Method: http.MethodGet, Path: "/api/v1/admin/users", Handler: a.listUsers},
		{Method: http.MethodPost, Path: "/api/v1/admin/users", Handler: a.createUser},
		{Method: http.MethodPatch, Path: "/api/v1/admin/users/:subject", Handler: a.updateUser},
		{Method: http.MethodGet, Path: "/api/v1/admin/roles", Handler: a.listRoles},
		{Method: http.MethodPost, Path: "/api/v1/admin/roles", Handler: a.createRole},
		{Method: http.MethodPatch, Path: "/api/v1/admin/roles/:name", Handler: a.updateRole},
		{Method: http.MethodGet, Path: "/api/v1/admin/permissions", Handler: a.listPermissions},
	}
}

type loginRequest struct {
	Subject  string `json:"subject"`
	Password string `json:"password"`
}
type identityResponse struct {
	Subject     string   `json:"subject"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
}
type userResponse struct {
	Subject  string   `json:"subject"`
	Disabled bool     `json:"disabled"`
	Roles    []string `json:"roles"`
}
type roleResponse struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}
type permissionResponse struct {
	Name string `json:"name"`
}
type managedUserRequest struct {
	Subject  string   `json:"subject"`
	Password string   `json:"password"`
	Roles    []string `json:"roles"`
	Disabled bool     `json:"disabled"`
}
type managedRoleRequest struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

func (a *RBACAPI) login(w http.ResponseWriter, r *http.Request) {
	var in loginRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Subject) == "" || in.Password == "" {
		writeErr(w, http.StatusBadRequest, "invalid_login", "subject and password are required")
		return
	}
	ip := clientIP(r)
	if a.rateLimited(ip) {
		writeErr(w, http.StatusTooManyRequests, "login_rate_limited", "too many failed login attempts")
		return
	}
	if a.service == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid credentials")
		return
	}
	token, identity, err := a.service.Login(r.Context(), strings.TrimSpace(in.Subject), in.Password, a.options.TTL)
	if err != nil {
		a.recordFailure(ip)
		writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid credentials")
		return
	}
	a.clearFailures(ip)
	http.SetCookie(w, &http.Cookie{Name: a.options.CookieName, Value: token, Path: "/", HttpOnly: true, Secure: a.options.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: int(a.options.TTL.Seconds()), Expires: a.now().Add(a.options.TTL)})
	writeData(w, http.StatusOK, publicIdentity(identity))
}

func (a *RBACAPI) me(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "identity required")
		return
	}
	writeData(w, http.StatusOK, publicIdentity(identity))
}

func publicIdentity(identity rbac.Identity) identityResponse {
	return identityResponse{Subject: identity.Subject, Roles: identity.Roles, Permissions: identity.Permissions}
}

func publicUser(user rbac.User) userResponse {
	return userResponse{Subject: user.Subject, Disabled: user.Disabled, Roles: user.Roles}
}
func publicUsers(users []rbac.User) []userResponse {
	result := make([]userResponse, 0, len(users))
	for _, user := range users {
		result = append(result, publicUser(user))
	}
	return result
}
func publicRole(role rbac.Role) roleResponse {
	return roleResponse{Name: role.Name, Permissions: role.Permissions}
}
func publicRoles(roles []rbac.Role) []roleResponse {
	result := make([]roleResponse, 0, len(roles))
	for _, role := range roles {
		result = append(result, publicRole(role))
	}
	return result
}
func publicPermissions(permissions []rbac.Permission) []permissionResponse {
	result := make([]permissionResponse, 0, len(permissions))
	for _, permission := range permissions {
		result = append(result, permissionResponse{Name: permission.Name})
	}
	return result
}

func (a *RBACAPI) logout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: a.options.CookieName, Value: "", Path: "/", HttpOnly: true, Secure: a.options.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	w.WriteHeader(http.StatusNoContent)
}

func (a *RBACAPI) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.service.ListUsers(r.Context())
	if err != nil {
		writeInternal(w)
		return
	}
	writeData(w, http.StatusOK, publicUsers(users))
}
func (a *RBACAPI) listRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := a.service.ListRoles(r.Context())
	if err != nil {
		writeInternal(w)
		return
	}
	writeData(w, http.StatusOK, publicRoles(roles))
}
func (a *RBACAPI) listPermissions(w http.ResponseWriter, r *http.Request) {
	permissions, err := a.service.ListPermissions(r.Context())
	if err != nil {
		writeInternal(w)
		return
	}
	writeData(w, http.StatusOK, publicPermissions(permissions))
}

func (a *RBACAPI) createUser(w http.ResponseWriter, r *http.Request) {
	var in managedUserRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Subject) == "" || in.Password == "" {
		writeErr(w, http.StatusBadRequest, "invalid_user", "subject and password are required")
		return
	}
	user, err := a.service.CreateManagedUser(r.Context(), strings.TrimSpace(in.Subject), in.Password, in.Roles)
	if err != nil {
		writeManagedError(w, err, "user")
		return
	}
	writeData(w, http.StatusCreated, publicUser(user))
}

func (a *RBACAPI) updateUser(w http.ResponseWriter, r *http.Request) {
	subject := strings.TrimSpace(pathvar.Vars(r)["subject"])
	var in managedUserRequest
	if subject == "" || json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "invalid_user", "subject is required")
		return
	}
	user, err := a.service.UpdateUser(r.Context(), subject, in.Password, in.Roles, in.Disabled)
	if err != nil {
		writeManagedError(w, err, "user")
		return
	}
	writeData(w, http.StatusOK, publicUser(user))
}

func (a *RBACAPI) createRole(w http.ResponseWriter, r *http.Request) {
	var in managedRoleRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, http.StatusBadRequest, "invalid_role", "name is required")
		return
	}
	role, err := a.service.CreateManagedRole(r.Context(), strings.TrimSpace(in.Name), in.Permissions)
	if err != nil {
		writeManagedError(w, err, "role")
		return
	}
	writeData(w, http.StatusCreated, publicRole(role))
}

func (a *RBACAPI) updateRole(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(pathvar.Vars(r)["name"])
	var in managedRoleRequest
	if name == "" || json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "invalid_role", "name is required")
		return
	}
	role, err := a.service.ReplaceRolePermissions(r.Context(), name, in.Permissions)
	if err != nil {
		writeManagedError(w, err, "role")
		return
	}
	writeData(w, http.StatusOK, publicRole(role))
}

func writeManagedError(w http.ResponseWriter, err error, entity string) {
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, rbac.ErrForbidden) {
		writeErr(w, http.StatusBadRequest, "unknown_role", "one or more roles are unknown")
		return
	}
	writeErr(w, http.StatusBadRequest, "invalid_"+entity, "invalid "+entity)
}
func writeInternal(w http.ResponseWriter) {
	writeErr(w, http.StatusInternalServerError, "internal_error", "request could not be completed")
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if strings.TrimSpace(r.RemoteAddr) != "" {
		return r.RemoteAddr
	}
	return "unknown"
}
func (a *RBACAPI) rateLimited(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneFailures(ip)
	return len(a.failures[ip]) >= 5
}
func (a *RBACAPI) recordFailure(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneFailures(ip)
	a.failures[ip] = append(a.failures[ip], a.now())
}
func (a *RBACAPI) clearFailures(ip string) { a.mu.Lock(); defer a.mu.Unlock(); delete(a.failures, ip) }
func (a *RBACAPI) pruneFailures(ip string) {
	cutoff := a.now().Add(-time.Minute)
	attempts := a.failures[ip]
	keep := attempts[:0]
	for _, attempt := range attempts {
		if attempt.After(cutoff) {
			keep = append(keep, attempt)
		}
	}
	if len(keep) == 0 {
		delete(a.failures, ip)
	} else {
		a.failures[ip] = keep
	}
}
