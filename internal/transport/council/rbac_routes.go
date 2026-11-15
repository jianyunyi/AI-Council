package council

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	identitysvc "github.com/aicouncil/aicouncil/internal/security/identity"
	oauthsvc "github.com/aicouncil/aicouncil/internal/security/oauth"
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
	pending  map[string]int
	now      func() time.Time
	identity *identitysvc.Service
	mailer   Mailer
	oauth    *oauthsvc.Service
}

const limiterClientLimit = 1024

func NewRBACAPI(service *rbac.Service, options SessionOptions) *RBACAPI {
	return &RBACAPI{service: service, options: normalizeSessionOptions(options), failures: make(map[string][]time.Time), pending: make(map[string]int), now: time.Now}
}

func NewRBACAPIWithIdentity(service *rbac.Service, options SessionOptions, identity *identitysvc.Service, mailer Mailer) *RBACAPI {
	return NewRBACAPIWithAuthFlows(service, options, identity, mailer, nil)
}

func NewRBACAPIWithAuthFlows(service *rbac.Service, options SessionOptions, identity *identitysvc.Service, mailer Mailer, oauth *oauthsvc.Service) *RBACAPI {
	a := NewRBACAPI(service, options)
	a.identity, a.mailer, a.oauth = identity, mailer, oauth
	return a
}

func (a *RBACAPI) Routes() []rest.Route {
	return []rest.Route{
		{Method: http.MethodPost, Path: "/api/v1/auth/login", Handler: a.login},
		{Method: http.MethodPost, Path: "/api/v1/auth/register", Handler: a.register},
		{Method: http.MethodPost, Path: "/api/v1/auth/verify-email", Handler: a.verifyEmail},
		{Method: http.MethodPost, Path: "/api/v1/auth/password-reset/request", Handler: a.requestPasswordReset},
		{Method: http.MethodPost, Path: "/api/v1/auth/password-reset/confirm", Handler: a.confirmPasswordReset},
		{Method: http.MethodGet, Path: "/api/v1/auth/oauth/providers", Handler: a.oauthProviders},
		{Method: http.MethodGet, Path: "/api/v1/auth/oauth/:provider/start", Handler: a.oauthStart},
		{Method: http.MethodGet, Path: "/api/v1/auth/oauth/:provider/callback", Handler: a.oauthCallback},
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
type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}
type identityTokenRequest struct {
	Token string `json:"token"`
}
type passwordResetRequest struct {
	Email string `json:"email"`
}
type Mailer interface {
	Send(context.Context, string, string, string) error
}
type identityResponse struct {
	Subject     string     `json:"subject"`
	Roles       []string   `json:"roles"`
	Permissions []string   `json:"permissions"`
	ExpiresAt   *time.Time `json:"expires_at"`
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
type managedUserPatchRequest struct {
	Password *string   `json:"password"`
	Roles    *[]string `json:"roles"`
	Disabled *bool     `json:"disabled"`
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
	if !a.admitLoginAttempt(ip) {
		writeErr(w, http.StatusTooManyRequests, "login_rate_limited", "too many failed login attempts")
		return
	}
	if a.service == nil {
		a.completeLoginAttempt(ip, true)
		writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid credentials")
		return
	}
	token, identity, err := a.service.Login(r.Context(), strings.TrimSpace(in.Subject), in.Password, a.options.TTL)
	if err != nil {
		a.completeLoginAttempt(ip, true)
		writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid credentials")
		return
	}
	a.completeLoginAttempt(ip, false)
	http.SetCookie(w, &http.Cookie{Name: a.options.CookieName, Value: token, Path: "/", HttpOnly: true, Secure: a.options.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: int(a.options.TTL.Seconds()), Expires: a.now().Add(a.options.TTL)})
	writeData(w, http.StatusOK, publicIdentity(identity))
}

func (a *RBACAPI) register(w http.ResponseWriter, r *http.Request) {
	var in registerRequest
	if a.identity == nil || a.mailer == nil {
		writeErr(w, http.StatusServiceUnavailable, "registration_unavailable", "registration is unavailable")
		return
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "invalid_registration", "email and password are required")
		return
	}
	token, err := a.identity.RegisterEmail(r.Context(), in.Email, in.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_registration", "email may already be registered or registration details are invalid")
		return
	}
	if err := a.mailer.Send(r.Context(), strings.TrimSpace(in.Email), "Verify your AI Council account", "Your one-time verification token is: "+token); err != nil {
		_ = a.identity.CancelUnverifiedRegistration(r.Context(), in.Email, token)
		slog.Error("registration verification email delivery failed")
		writeInternal(w)
		return
	}
	writeData(w, http.StatusAccepted, map[string]string{"status": "verification_sent"})
}

func (a *RBACAPI) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var in identityTokenRequest
	if a.identity == nil || json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "invalid_token", "verification token is invalid or expired")
		return
	}
	if err := a.identity.VerifyEmail(r.Context(), in.Token); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_token", "verification token is invalid or expired")
		return
	}
	writeData(w, http.StatusOK, map[string]string{"status": "verified"})
}

func (a *RBACAPI) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var in passwordResetRequest
	if a.identity == nil || a.mailer == nil {
		writeErr(w, http.StatusServiceUnavailable, "password_reset_unavailable", "password reset is unavailable")
		return
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "invalid_reset_request", "email is required")
		return
	}
	token, err := a.identity.RequestPasswordReset(r.Context(), in.Email)
	if err != nil {
		writeInternal(w)
		return
	}
	if token != "" {
		if err := a.mailer.Send(r.Context(), strings.TrimSpace(in.Email), "Reset your AI Council password", "Your one-time password reset token is: "+token); err != nil {
			// Keep the response indistinguishable from an unknown email address.
			slog.Error("password reset email delivery failed")
		}
	}
	writeData(w, http.StatusAccepted, map[string]string{"status": "if_account_exists_email_sent"})
}

func (a *RBACAPI) confirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if a.identity == nil || json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "invalid_reset", "reset token or password is invalid")
		return
	}
	if err := a.identity.ResetPassword(r.Context(), in.Token, in.Password); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_reset", "reset token or password is invalid")
		return
	}
	writeData(w, http.StatusOK, map[string]string{"status": "password_updated"})
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
	return identityResponse{Subject: identity.Subject, Roles: identity.Roles, Permissions: identity.Permissions, ExpiresAt: identity.ExpiresAt}
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

func (a *RBACAPI) logout(w http.ResponseWriter, r *http.Request) {
	if err := a.revokeLogoutToken(r); err != nil {
		writeInternal(w)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: a.options.CookieName, Value: "", Path: "/", HttpOnly: true, Secure: a.options.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	w.WriteHeader(http.StatusNoContent)
}

func (a *RBACAPI) revokeLogoutToken(r *http.Request) error {
	if a.service == nil {
		return errors.New("rbac service unavailable")
	}
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(raw) >= 7 && strings.EqualFold(raw[:7], "bearer ") {
		token := strings.TrimSpace(raw[7:])
		if token != "" {
			revoked, err := a.service.RevokePresentedToken(r.Context(), token)
			if err != nil {
				return err
			}
			if revoked {
				return nil
			}
		}
	}
	cookie, err := r.Cookie(a.options.CookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return nil
	}
	_, err = a.service.RevokePresentedToken(r.Context(), cookie.Value)
	return err
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
	var in managedUserPatchRequest
	if subject == "" || json.NewDecoder(r.Body).Decode(&in) != nil || (in.Password == nil && in.Roles == nil && in.Disabled == nil) {
		writeErr(w, http.StatusBadRequest, "invalid_user", "subject and at least one update are required")
		return
	}
	user, err := a.service.PatchUser(r.Context(), subject, in.Password, in.Roles, in.Disabled)
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
	if name == "" || json.NewDecoder(r.Body).Decode(&in) != nil || in.Permissions == nil {
		writeErr(w, http.StatusBadRequest, "invalid_role", "name and permissions are required")
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
func (a *RBACAPI) admitLoginAttempt(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneAllFailures()
	if _, known := a.failures[ip]; !known && a.pending[ip] == 0 && a.limiterClients() >= limiterClientLimit {
		return false
	}
	if len(a.failures[ip])+a.pending[ip] >= 5 {
		return false
	}
	a.pending[ip]++
	return true
}
func (a *RBACAPI) completeLoginAttempt(ip string, failed bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneAllFailures()
	if a.pending[ip] > 1 {
		a.pending[ip]--
	} else {
		delete(a.pending, ip)
	}
	if failed {
		a.failures[ip] = append(a.failures[ip], a.now())
	} else {
		delete(a.failures, ip)
	}
}
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

func (a *RBACAPI) pruneAllFailures() {
	for ip := range a.failures {
		a.pruneFailures(ip)
	}
}

func (a *RBACAPI) limiterClients() int {
	clients := len(a.failures)
	for ip := range a.pending {
		if _, exists := a.failures[ip]; !exists {
			clients++
		}
	}
	return clients
}
