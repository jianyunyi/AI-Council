package council

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/aicouncil/aicouncil/internal/security/rbac"
	storage "github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/pathvar"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestBearerAuthRejectsMissingAndAcceptsValidToken(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	h := BearerAuth("secret")(next)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, 401, w.Code)
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, 204, w.Code)
}

func TestBearerAuthAllowsUnauthenticatedHealthCheck(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := BearerAuth("secret")(next)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusNoContent, w.Code)
}

func newRBACService(t *testing.T) *rbac.Service {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "rbac.sqlite"))
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return rbac.New(db)
}

func routeHandler(t *testing.T, routes []rest.Route, method, path string) http.HandlerFunc {
	t.Helper()
	for _, item := range routes {
		if item.Method == method && item.Path == path {
			return item.Handler
		}
	}
	t.Fatalf("missing route %s %s", method, path)
	return nil
}

func TestRBACLoginSetsSecureSessionCookieAndMeUsesIt(t *testing.T) {
	service := newRBACService(t)
	require.NoError(t, service.CreateUserWithPassword(context.Background(), "alice", "password"))
	opts := SessionOptions{CookieName: "session", CookieSecure: false, TTL: 2 * time.Hour}
	api := NewRBACAPI(service, opts)

	login := httptest.NewRecorder()
	routeHandler(t, api.Routes(), http.MethodPost, "/api/v1/auth/login")(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"subject":"alice","password":"password"}`)))
	require.Equal(t, http.StatusOK, login.Code)
	cookies := login.Result().Cookies()
	require.Len(t, cookies, 1)
	cookie := cookies[0]
	require.Equal(t, "session", cookie.Name)
	require.True(t, cookie.HttpOnly)
	require.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	require.Equal(t, "/", cookie.Path)
	require.False(t, cookie.Secure)
	require.Greater(t, cookie.MaxAge, 0)
	require.WithinDuration(t, time.Now().Add(2*time.Hour), cookie.Expires, 2*time.Second)

	me := RBACAuthWithOptions(service, "", opts)(routeHandler(t, api.Routes(), http.MethodGet, "/api/v1/auth/me"))
	meRec := httptest.NewRecorder()
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.AddCookie(cookie)
	me.ServeHTTP(meRec, meReq)
	require.Equal(t, http.StatusOK, meRec.Code)
	require.Contains(t, meRec.Body.String(), `"subject":"alice"`)

	logout := httptest.NewRecorder()
	routeHandler(t, api.Routes(), http.MethodPost, "/api/v1/auth/logout")(logout, httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil))
	require.Equal(t, http.StatusNoContent, logout.Code)
	require.Equal(t, -1, logout.Result().Cookies()[0].MaxAge)
}

func TestRBACLogoutRevokesCookieAndBearerSessionsIdempotently(t *testing.T) {
	service := newRBACService(t)
	ctx := context.Background()
	require.NoError(t, service.CreateUserWithPassword(ctx, "alice", "password"))
	api := NewRBACAPI(service, SessionOptions{CookieSecure: false, TTL: time.Hour})
	login := routeHandler(t, api.Routes(), http.MethodPost, "/api/v1/auth/login")
	issue := func() string {
		rec := httptest.NewRecorder()
		login(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"subject":"alice","password":"password"}`)))
		require.Equal(t, http.StatusOK, rec.Code)
		return rec.Result().Cookies()[0].Value
	}
	cookieToken, bearerToken := issue(), issue()
	logout := routeHandler(t, api.Routes(), http.MethodPost, "/api/v1/auth/logout")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	request.Header.Set("Authorization", "Bearer "+bearerToken)
	request.AddCookie(&http.Cookie{Name: "aicouncil_session", Value: cookieToken})
	rec := httptest.NewRecorder()
	logout(rec, request)
	require.Equal(t, http.StatusNoContent, rec.Code)
	_, err := service.Authenticate(ctx, bearerToken)
	require.ErrorIs(t, err, rbac.ErrUnauthorized)
	_, err = service.Authenticate(ctx, cookieToken)
	require.NoError(t, err, "a valid Bearer token must be revoked before the cookie token")

	rec = httptest.NewRecorder()
	logout(rec, request)
	require.Equal(t, http.StatusNoContent, rec.Code)
	cookieLogout := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	cookieLogout.AddCookie(&http.Cookie{Name: "aicouncil_session", Value: cookieToken})
	rec = httptest.NewRecorder()
	logout(rec, cookieLogout)
	require.Equal(t, http.StatusNoContent, rec.Code)
	_, err = service.Authenticate(ctx, cookieToken)
	require.ErrorIs(t, err, rbac.ErrUnauthorized)
	replay := RBACAuth(service, "")(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/task-1", nil)
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	replay(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestSessionOptionsZeroValueUsesSecureDefaults(t *testing.T) {
	options := normalizeSessionOptions(SessionOptions{})
	require.Equal(t, "aicouncil_session", options.CookieName)
	require.True(t, options.CookieSecure)
	require.Positive(t, options.TTL)
}

func TestRBACAuthUsesBearerBeforeCookieAndFallsBackOnInvalidBearer(t *testing.T) {
	service := newRBACService(t)
	ctx := context.Background()
	require.NoError(t, service.CreateUser(ctx, "bearer", "desktop-token"))
	require.NoError(t, service.CreateRole(ctx, "reader"))
	require.NoError(t, service.AssignRole(ctx, "bearer", "reader"))
	require.NoError(t, service.GrantPermission(ctx, "reader", "task:read"))
	require.NoError(t, service.CreateUserWithPassword(ctx, "cookie", "password"))
	require.NoError(t, service.AssignRole(ctx, "cookie", "reader"))
	opts := SessionOptions{CookieName: "session", CookieSecure: false, TTL: time.Hour}
	token, _, err := service.Login(ctx, "cookie", "password", opts.TTL)
	require.NoError(t, err)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := IdentityFromRequest(r)
		require.True(t, ok)
		_, _ = w.Write([]byte(identity.Subject))
	})
	h := RBACAuthWithOptions(service, "", opts)(next)
	request := func(bearer string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/one", nil)
		req.RemoteAddr = net.JoinHostPort("192.0.2.7", "1234")
		req.AddCookie(&http.Cookie{Name: opts.CookieName, Value: token})
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		h.ServeHTTP(rec, req)
		return rec
	}
	require.Equal(t, "bearer", request("desktop-token").Body.String())
	require.Equal(t, "cookie", request("wrong-token").Body.String())
}

func TestRBACAuthEnforcesTaskAndAdminPermissionsAndKeepsHealthPublic(t *testing.T) {
	service := newRBACService(t)
	ctx := context.Background()
	require.NoError(t, service.CreateUser(ctx, "reader", "reader-token"))
	require.NoError(t, service.CreateRole(ctx, "reader"))
	require.NoError(t, service.AssignRole(ctx, "reader", "reader"))
	require.NoError(t, service.GrantPermission(ctx, "reader", "task:read"))
	require.NoError(t, service.CreateUser(ctx, "admin", "admin-token"))
	require.NoError(t, service.CreateRole(ctx, "admin"))
	require.NoError(t, service.AssignRole(ctx, "admin", "admin"))
	require.NoError(t, service.GrantPermission(ctx, "admin", "admin:*"))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := RBACAuth(service, "")(next)
	call := func(method, path, token string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		h.ServeHTTP(rec, req)
		return rec
	}
	require.Equal(t, http.StatusNoContent, call(http.MethodGet, "/healthz", "").Code)
	require.Equal(t, http.StatusNoContent, call(http.MethodGet, "/metrics", "").Code)
	require.Equal(t, http.StatusNoContent, call(http.MethodGet, "/api/v1/tasks/task-1", "reader-token").Code)
	denied := call(http.MethodPost, "/api/v1/tasks/task-1/approve", "reader-token")
	require.Equal(t, http.StatusForbidden, denied.Code)
	require.Contains(t, denied.Body.String(), `"code":"forbidden"`)
	require.Equal(t, http.StatusForbidden, call(http.MethodGet, "/api/v1/admin/users", "reader-token").Code)
	require.Equal(t, http.StatusNoContent, call(http.MethodGet, "/api/v1/admin/users", "admin-token").Code)
}

func TestRBACAuthDeniesUnknownTaskAndAdminDescendants(t *testing.T) {
	service := newRBACService(t)
	ctx := context.Background()
	require.NoError(t, service.CreateUser(ctx, "reader", "reader-token"))
	require.NoError(t, service.CreateRole(ctx, "reader"))
	require.NoError(t, service.AssignRole(ctx, "reader", "reader"))
	require.NoError(t, service.GrantPermission(ctx, "reader", "task:read"))
	require.NoError(t, service.CreateUser(ctx, "admin", "admin-token"))
	require.NoError(t, service.CreateRole(ctx, "admin"))
	require.NoError(t, service.AssignRole(ctx, "admin", "admin"))
	require.NoError(t, service.GrantPermission(ctx, "admin", "admin:users"))
	h := RBACAuth(service, "")(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	call := func(path, token string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		h(rec, req)
		return rec
	}
	require.Equal(t, http.StatusForbidden, call("/api/v1/tasks/id/sensitive", "reader-token").Code)
	require.Equal(t, http.StatusForbidden, call("/api/v1/admin/users/alice/secret", "admin-token").Code)
	patch := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, path, nil)
		req.Header.Set("Authorization", "Bearer admin-token")
		h(rec, req)
		return rec
	}
	require.Equal(t, http.StatusForbidden, patch("/api/v1/admin/users").Code)
	require.Equal(t, http.StatusForbidden, patch("/api/v1/admin/roles").Code)
}

func TestRBACLoginRateLimitAndSuccessfulLoginClearsFailures(t *testing.T) {
	service := newRBACService(t)
	require.NoError(t, service.CreateUserWithPassword(context.Background(), "alice", "password"))
	api := NewRBACAPI(service, SessionOptions{CookieSecure: false, TTL: time.Hour})
	login := routeHandler(t, api.Routes(), http.MethodPost, "/api/v1/auth/login")
	call := func(password string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"subject":"alice","password":"`+password+`"}`))
		req.RemoteAddr = net.JoinHostPort("192.0.2.10", "6789")
		login(rec, req)
		return rec
	}
	for range 4 {
		require.Equal(t, http.StatusUnauthorized, call("wrong").Code)
	}
	require.Equal(t, http.StatusOK, call("password").Code)
	for range 5 {
		require.Equal(t, http.StatusUnauthorized, call("wrong").Code)
	}
	limited := call("wrong")
	require.Equal(t, http.StatusTooManyRequests, limited.Code)
	require.Contains(t, limited.Body.String(), "login_rate_limited")
}

func TestRBACLoginRateLimiterAdmitsAtMostFiveConcurrentFailures(t *testing.T) {
	service := newRBACService(t)
	require.NoError(t, service.CreateUserWithPassword(context.Background(), "alice", "password"))
	api := NewRBACAPI(service, SessionOptions{CookieSecure: false, TTL: time.Hour})
	login := routeHandler(t, api.Routes(), http.MethodPost, "/api/v1/auth/login")
	const attempts = 16
	start := make(chan struct{})
	results := make(chan int, attempts)
	var group sync.WaitGroup
	for range attempts {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"subject":"alice","password":"wrong"}`))
			req.RemoteAddr = net.JoinHostPort("192.0.2.44", "1234")
			login(rec, req)
			results <- rec.Code
		}()
	}
	close(start)
	group.Wait()
	close(results)
	unauthorized, limited := 0, 0
	for status := range results {
		if status == http.StatusUnauthorized {
			unauthorized++
		}
		if status == http.StatusTooManyRequests {
			limited++
		}
	}
	require.Equal(t, 5, unauthorized)
	require.Equal(t, attempts-5, limited)
}

func TestRBACLoginLimiterGloballyPrunesExpiredClientsWithoutDroppingPending(t *testing.T) {
	api := NewRBACAPI(nil, SessionOptions{CookieSecure: false, TTL: time.Hour})
	now := time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC)
	api.now = func() time.Time { return now }
	api.failures["stale"] = []time.Time{now.Add(-2 * time.Minute)}
	api.pending["pending"] = 1
	require.True(t, api.admitLoginAttempt("fresh"))
	require.NotContains(t, api.failures, "stale")
	require.Equal(t, 1, api.pending["pending"])
}

func TestRBACAdminUserAndRoleHandlersDoNotExposeSecrets(t *testing.T) {
	service := newRBACService(t)
	ctx := context.Background()
	require.NoError(t, service.CreateUser(ctx, "admin", "token"))
	require.NoError(t, service.CreateRole(ctx, "admin"))
	require.NoError(t, service.AssignRole(ctx, "admin", "admin"))
	require.NoError(t, service.GrantPermission(ctx, "admin", "admin:*"))
	api := NewRBACAPI(service, SessionOptions{CookieSecure: false, TTL: time.Hour})
	secured := func(handler http.HandlerFunc) http.HandlerFunc { return RBACAuth(service, "")(handler) }
	call := func(method, path, body string, handler http.HandlerFunc) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer token")
		secured(handler)(rec, req)
		return rec
	}
	createRole := call(http.MethodPost, "/api/v1/admin/roles", `{"name":"operator","permissions":["task:read"]}`, routeHandler(t, api.Routes(), http.MethodPost, "/api/v1/admin/roles"))
	require.Equal(t, http.StatusCreated, createRole.Code)
	createUser := call(http.MethodPost, "/api/v1/admin/users", `{"subject":"bob","password":"secret","roles":["operator"]}`, routeHandler(t, api.Routes(), http.MethodPost, "/api/v1/admin/users"))
	require.Equal(t, http.StatusCreated, createUser.Code)
	require.NotContains(t, createUser.Body.String(), "secret")
	var body map[string]any
	require.NoError(t, json.Unmarshal(createUser.Body.Bytes(), &body))
	require.NotNil(t, body["data"])
	users := call(http.MethodGet, "/api/v1/admin/users", "", routeHandler(t, api.Routes(), http.MethodGet, "/api/v1/admin/users"))
	require.Equal(t, http.StatusOK, users.Code)
	updateUserRequest := pathvar.WithVars(httptest.NewRequest(http.MethodPatch, "/api/v1/admin/users/bob", bytes.NewBufferString(`{"disabled":true,"roles":["operator"]}`)), map[string]string{"subject": "bob"})
	updateUserRequest.Header.Set("Authorization", "Bearer token")
	updateUser := httptest.NewRecorder()
	secured(routeHandler(t, api.Routes(), http.MethodPatch, "/api/v1/admin/users/:subject"))(updateUser, updateUserRequest)
	require.Equal(t, http.StatusOK, updateUser.Code)
	require.Contains(t, updateUser.Body.String(), `"disabled":true`)
	updateRoleRequest := pathvar.WithVars(httptest.NewRequest(http.MethodPatch, "/api/v1/admin/roles/operator", bytes.NewBufferString(`{"permissions":["task:write"]}`)), map[string]string{"name": "operator"})
	updateRoleRequest.Header.Set("Authorization", "Bearer token")
	updateRole := httptest.NewRecorder()
	secured(routeHandler(t, api.Routes(), http.MethodPatch, "/api/v1/admin/roles/:name"))(updateRole, updateRoleRequest)
	require.Equal(t, http.StatusOK, updateRole.Code)
	unknown := call(http.MethodPost, "/api/v1/admin/users", `{"subject":"eve","password":"secret","roles":["missing"]}`, routeHandler(t, api.Routes(), http.MethodPost, "/api/v1/admin/users"))
	require.Equal(t, http.StatusBadRequest, unknown.Code)
	require.Contains(t, unknown.Body.String(), "unknown_role")
}

func TestRBACAdminPatchPreservesOmittedFieldsAndRequiresNonEmptyPatch(t *testing.T) {
	service := newRBACService(t)
	ctx := context.Background()
	require.NoError(t, service.CreateUser(ctx, "admin", "token"))
	require.NoError(t, service.CreateRole(ctx, "admin"))
	require.NoError(t, service.GrantPermission(ctx, "admin", "admin:*"))
	require.NoError(t, service.AssignRole(ctx, "admin", "admin"))
	require.NoError(t, service.CreateRole(ctx, "operator"))
	_, err := service.CreateManagedUser(ctx, "bob", "old", []string{"operator"})
	require.NoError(t, err)
	_, err = service.UpdateUser(ctx, "bob", "", []string{"operator"}, true)
	require.NoError(t, err)
	api := NewRBACAPI(service, SessionOptions{CookieSecure: false, TTL: time.Hour})
	patchUser := RBACAuth(service, "")(routeHandler(t, api.Routes(), http.MethodPatch, "/api/v1/admin/users/:subject"))
	call := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := pathvar.WithVars(httptest.NewRequest(http.MethodPatch, "/api/v1/admin/users/bob", bytes.NewBufferString(body)), map[string]string{"subject": "bob"})
		req.Header.Set("Authorization", "Bearer token")
		patchUser(rec, req)
		return rec
	}
	passwordOnly := call(`{"password":"new"}`)
	require.Equal(t, http.StatusOK, passwordOnly.Code)
	require.Contains(t, passwordOnly.Body.String(), `"disabled":true`)
	require.Contains(t, passwordOnly.Body.String(), `"roles":["operator"]`)
	require.Equal(t, http.StatusBadRequest, call(`{}`).Code)
	rolesOnly := call(`{"roles":[]}`)
	require.Equal(t, http.StatusOK, rolesOnly.Code)
	require.Contains(t, rolesOnly.Body.String(), `"roles":[]`)
	patchRole := RBACAuth(service, "")(routeHandler(t, api.Routes(), http.MethodPatch, "/api/v1/admin/roles/:name"))
	rec := httptest.NewRecorder()
	req := pathvar.WithVars(httptest.NewRequest(http.MethodPatch, "/api/v1/admin/roles/operator", bytes.NewBufferString(`{}`)), map[string]string{"name": "operator"})
	req.Header.Set("Authorization", "Bearer token")
	patchRole(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
