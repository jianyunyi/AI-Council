# Web RBAC Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver same-origin password login, permission-scoped Council APIs, and web account/user-role management without exposing browser session tokens to JavaScript.

**Architecture:** The SQLite RBAC service remains the source of truth for users, roles, permissions, and revocable access tokens. A Council HTTP authentication layer reads an HttpOnly session cookie or a desktop/CLI Bearer token, maps each protected method/path to a permission, and passes the authenticated identity to handlers. The statically exported Next.js interface calls the same origin with credentials, renders UI from `/auth/me`, and hides or blocks controls not covered by the current permissions.

**Tech Stack:** Go, go-zero REST, GORM/SQLite, Argon2id, React 18, Next.js 14 static export, TypeScript, Vitest, Playwright.

---

## Execution status

- [x] Task 1: service implementation, specification review, quality review (through `dba63c55`).
- [x] Task 2: REST, session security fixes and real-stack log regression (`502bb6fd`); specification and quality reviews approved.
- [ ] Task 3: deployment/bootstrap integration (in progress).
- [ ] Task 4: web session/API foundation (in progress).
- [ ] Task 5: account and administration screens.
- [ ] Task 6: real-service browser harness (in progress); browser acceptance and final verification pending UI integration.

Execution notes: use the installed `node` executable if the cached runtime path shown below is unavailable. The tracked Caddy configuration is `deploy/Caddyfile`. The service uses safe transport projections for JSON; the implemented managed-user methods return projections rather than only errors.

The approved design remains authoritative where the illustrative task snippets omit behavior: logout must revoke persisted sessions, identity responses include the actual `expires_at`, PATCH preserves omitted fields, and the server supplies a standard permission catalog. Both `admin:users` and `admin:roles` can expose the administration navigation, with each section independently guarded. Windows browser verification can use installed Edge (`PLAYWRIGHT_CHANNEL=msedge`, new headless mode), which passed a launch probe.

## File structure

- Modify: `internal/security/rbac/service.go` — password login, wildcard-aware authorization, and safe RBAC projections/mutations.
- Modify: `internal/security/rbac/service_test.go` — service authorization, login, role and user mutation coverage.
- Create: `internal/transport/council/rbac_routes.go` — auth and admin REST request/response handlers only.
- Modify: `internal/transport/council/auth.go` — cookie/Bearer extraction, route-permission policy, identity context, and bounded login rate limiting.
- Modify: `internal/transport/council/http.go` — register public auth endpoints, permission-protected API routes, and cookie configuration.
- Modify: `internal/transport/council/auth_test.go` — real HTTP middleware tests for cookies, permissions, and login abuse protection.
- Modify: `internal/transport/council/routes_test.go` — full server route tests for admin lifecycle and task permission denials.
- Modify: `cmd/council-server/main.go` — explicit RBAC/password bootstrap and `AUTH_COOKIE_SECURE` deployment configuration.
- Modify: `cmd/council-server/main_test.go` — configuration parsing and RBAC server selection tests.
- Modify: `web/lib/types.ts` — public identity, user, role, permission, and request DTO types.
- Modify: `web/lib/api.ts` and `web/lib/api.test.ts` — same-origin credentials and typed auth/admin calls; retain desktop Bearer injection.
- Create: `web/components/auth-session.tsx` and `web/components/auth-session.test.tsx` — `AuthProvider`, permission hook, loading and access guards.
- Modify: `web/app/layout.tsx` — provider wiring and permission-aware navigation.
- Create: `web/app/login/page.tsx` and `web/app/login/page.test.tsx` — accessible password sign-in page.
- Create: `web/app/account/page.tsx` and `web/app/account/page.test.tsx` — authenticated account identity and role/permission display.
- Create: `web/app/admin/users/page.tsx` and `web/app/admin/users/page.test.tsx` — user, role, disabled-state and password management form.
- Modify: `web/app/globals.css` and `web/app/globals.test.ts` — compact management-table, status, and empty-state styles aligned to the command-center theme.
- Modify: `web/e2e/council.spec.ts` — browser login and admin-permission acceptance path.
- Modify: `README.md` and `deploy/caddy/Caddyfile` if present — production cookie/TLS bootstrap instructions and same-origin reverse-proxy example.

### Task 1: Extend the RBAC service as the single permission authority

**Files:**
- Modify: `internal/security/rbac/service.go`
- Modify: `internal/security/rbac/service_test.go`

- [ ] **Step 1: Write the failing service tests for password login and wildcard permission matching.**

```go
func TestLoginAndPermissionWildcard(t *testing.T) {
    service := newTestService(t)
    ctx := context.Background()
    require.NoError(t, service.CreateUserWithPassword(ctx, "alice", "correct horse"))
    require.NoError(t, service.CreateRole(ctx, "administrator"))
    require.NoError(t, service.GrantPermission(ctx, "administrator", "admin:*"))
    require.NoError(t, service.AssignRole(ctx, "alice", "administrator"))

    token, identity, err := service.Login(ctx, "alice", "correct horse", time.Hour)
    require.NoError(t, err)
    require.Equal(t, "alice", identity.Subject)
    require.NotEmpty(t, token)
    require.NoError(t, service.AuthorizePermission(ctx, token, "admin:users"))
    require.NoError(t, service.AuthorizePermission(ctx, token, "admin:roles"))
    _, _, err = service.Login(ctx, "alice", "wrong", time.Hour)
    require.ErrorIs(t, err, ErrUnauthorized)
}
```

- [ ] **Step 2: Run the focused test and verify it fails because `Login` does not exist.**

Run: `$env:GOCACHE='F:\AI-council\.tmp-go-cache'; go test ./internal/security/rbac -run TestLoginAndPermissionWildcard -count=1`

Expected: compile failure mentioning `service.Login`.

- [ ] **Step 3: Add minimal service methods and wildcard matching.**

```go
func (s *Service) Login(ctx context.Context, subject, password string, ttl time.Duration) (string, Identity, error) {
    var user sqlite.UserRecord
    if err := s.db.WithContext(ctx).Where("subject = ?", subject).First(&user).Error; err != nil || user.Disabled || user.PasswordHash == nil || VerifyPassword(*user.PasswordHash, password) != nil {
        return "", Identity{}, ErrUnauthorized
    }
    token, _, err := s.IssueToken(ctx, user.ID, ttl)
    if err != nil { return "", Identity{}, err }
    identity, err := s.identityForUser(ctx, user.ID)
    return token, identity, err
}

func grants(granted, required string) bool {
    return granted == required || (strings.HasSuffix(granted, ":*") && strings.HasPrefix(required, strings.TrimSuffix(granted, "*")))
}
```

Replace the equality comparison inside `AuthorizePermission` with `grants(granted, permission)`. Add `ListUsers`, `ListRoles`, `ListPermissions`, `UpdateUser`, and `ReplaceRolePermissions` in this package; each must return projection structs containing only subject, disabled, roles and permissions—never password/token hashes. `UpdateUser` must update password only when a non-empty new value is supplied and replace roles transactionally after confirming each named role exists.

- [ ] **Step 4: Add persistence-boundary tests for the admin projections/mutations.**

```go
func TestUpdateUserReplacesRolesAndDoesNotExposeSecrets(t *testing.T) {
    service := newTestService(t)
    ctx := context.Background()
    require.NoError(t, service.CreateUserWithPassword(ctx, "alice", "old-password"))
    require.NoError(t, service.CreateRole(ctx, "reader"))
    require.NoError(t, service.CreateRole(ctx, "operator"))
    require.NoError(t, service.UpdateUser(ctx, "alice", UpdateUserInput{Disabled: false, Password: "new-password", Roles: []string{"operator"}}))
    users, err := service.ListUsers(ctx)
    require.NoError(t, err)
    require.Equal(t, User{Subject: "alice", Disabled: false, Roles: []string{"operator"}}, users[0])
    _, _, err = service.Login(ctx, "alice", "old-password", time.Hour)
    require.ErrorIs(t, err, ErrUnauthorized)
}
```

- [ ] **Step 5: Run the RBAC package tests.**

Run: `$env:GOCACHE='F:\AI-council\.tmp-go-cache'; go test ./internal/security/rbac -count=1`

Expected: `ok github.com/aicouncil/aicouncil/internal/security/rbac`.

- [ ] **Step 6: Commit the service boundary.**

```powershell
git add internal/security/rbac/service.go internal/security/rbac/service_test.go
git commit -m "feat: add rbac password login and management service"
```

### Task 2: Add cookie/Bearer authentication and permission-protected REST routes

**Files:**
- Modify: `internal/transport/council/auth.go`
- Create: `internal/transport/council/rbac_routes.go`
- Modify: `internal/transport/council/http.go`
- Modify: `internal/transport/council/auth_test.go`
- Modify: `internal/transport/council/routes_test.go`

- [ ] **Step 1: Write HTTP tests describing public login, HttpOnly cookie, identity, logout, and denial behavior.**

```go
func TestSessionLoginMeAndLogout(t *testing.T) {
    server := newRBACServer(t, "alice", "correct horse", "task:read")
    login := httptest.NewRecorder()
    server.ServeHTTP(login, jsonRequest(http.MethodPost, "/api/v1/auth/login", `{"subject":"alice","password":"correct horse"}`))
    require.Equal(t, http.StatusOK, login.Code)
    cookie := login.Result().Cookies()[0]
    require.True(t, cookie.HttpOnly)
    require.Equal(t, http.SameSiteStrictMode, cookie.SameSite)

    me := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
    req.AddCookie(cookie)
    server.ServeHTTP(me, req)
    require.Equal(t, http.StatusOK, me.Code)

    denied := httptest.NewRecorder()
    server.ServeHTTP(denied, httptest.NewRequest(http.MethodPost, "/api/v1/tasks", nil))
    require.Equal(t, http.StatusUnauthorized, denied.Code)
}
```

- [ ] **Step 2: Run the focused transport test and verify it fails because auth routes do not exist.**

Run: `$env:GOCACHE='F:\AI-council\.tmp-go-cache'; go test ./internal/transport/council -run TestSessionLoginMeAndLogout -count=1`

Expected: failure because `/api/v1/auth/login` is not registered.

- [ ] **Step 3: Implement a single route policy and session extractor in `auth.go`.**

```go
var routePermissions = map[string]string{
    "GET /api/v1/workspaces": "workspace:read", "POST /api/v1/workspaces": "workspace:write",
    "GET /api/v1/tasks/": "task:read", "POST /api/v1/tasks": "task:write",
    "POST /api/v1/tasks/": "task:write", "GET /api/v1/admin/users": "admin:users",
    "POST /api/v1/admin/users": "admin:users", "PATCH /api/v1/admin/users/": "admin:users",
    "GET /api/v1/admin/roles": "admin:roles", "POST /api/v1/admin/roles": "admin:roles",
    "PATCH /api/v1/admin/roles/": "admin:roles", "GET /api/v1/admin/permissions": "admin:permissions",
}

type SessionOptions struct { CookieName string; CookieSecure bool; TTL time.Duration }
```

Resolve tokens in this order: valid `Authorization: Bearer` (desktop/CLI) then the configured cookie. Exempt only `/healthz`, `/metrics`, and `POST /api/v1/auth/login`; `POST /api/v1/auth/logout` may clear the cookie without an identity. Write `401 unauthorized` for absent/invalid credentials and `403 forbidden` for a valid identity lacking the mapped permission. Store `rbac.Identity` in a private request context key so `/auth/me` never re-parses credentials.

- [ ] **Step 4: Implement auth and admin handlers in `rbac_routes.go`.**

```go
type loginRequest struct { Subject string `json:"subject"`; Password string `json:"password"` }
func (a *RBACAPI) login(w http.ResponseWriter, r *http.Request) {
    var in loginRequest
    if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Subject) == "" || in.Password == "" {
        writeErr(w, http.StatusBadRequest, "invalid_login", "subject and password are required"); return
    }
    token, identity, err := a.service.Login(r.Context(), strings.TrimSpace(in.Subject), in.Password, a.options.TTL)
    if err != nil { writeErr(w, http.StatusUnauthorized, "invalid_credentials", "invalid credentials"); return }
    http.SetCookie(w, sessionCookie(a.options, token, a.options.TTL))
    writeData(w, http.StatusOK, identity)
}
```

Add handlers for `GET /api/v1/auth/me`, `POST /api/v1/auth/logout`, `GET|POST|PATCH /api/v1/admin/users`, `GET|POST|PATCH /api/v1/admin/roles`, and `GET /api/v1/admin/permissions`. Validate required strings and reject unknown role names with a stable 400 error; never emit raw GORM or password errors. Apply an in-memory per-client-IP login limiter of five failed attempts per minute and return `429 login_rate_limited`; clear a client’s counter after successful login.

- [ ] **Step 5: Register routes and make both HTTP/TLS constructors accept the same RBAC options.**

```go
func addRBACRoutes(server *rest.Server, api *API, service *rbac.Service, options SessionOptions) {
    rbacAPI := NewRBACAPI(service, options)
    for _, route := range append(rbacAPI.Routes(), api.Routes()...) { server.AddRoute(route) }
}
```

Use the new authentication middleware in `NewServerWithAPIAndRBAC` and `NewTLSServerWithAPIAndRBAC`; preserve existing static `BearerAuth` constructors unchanged. Ensure `CookieSecure` is `true` by default, including TLS server construction.

- [ ] **Step 6: Run the transport package tests.**

Run: `$env:GOCACHE='F:\AI-council\.tmp-go-cache'; go test ./internal/transport/council -count=1`

Expected: `ok github.com/aicouncil/aicouncil/internal/transport/council`.

- [ ] **Step 7: Commit the REST security boundary.**

```powershell
git add internal/transport/council/auth.go internal/transport/council/rbac_routes.go internal/transport/council/http.go internal/transport/council/auth_test.go internal/transport/council/routes_test.go
git commit -m "feat: add cookie sessions and permission rbac routes"
```

### Task 3: Make RBAC deployment explicit and safe

**Files:**
- Modify: `cmd/council-server/main.go`
- Modify: `cmd/council-server/main_test.go`
- Modify: `README.md`
- Modify: `deploy/caddy/Caddyfile` (only if this tracked path exists)

- [ ] **Step 1: Write configuration tests for secure defaults and deliberate local downgrade.**

```go
func TestCookieSecureFromEnvironment(t *testing.T) {
    t.Setenv("AUTH_COOKIE_SECURE", "")
    require.True(t, authCookieSecure())
    t.Setenv("AUTH_COOKIE_SECURE", "false")
    require.False(t, authCookieSecure())
}
```

- [ ] **Step 2: Run the focused command test and verify it fails because the helper is absent.**

Run: `$env:GOCACHE='F:\AI-council\.tmp-go-cache'; go test ./cmd/council-server -run TestCookieSecureFromEnvironment -count=1`

Expected: compile failure mentioning `authCookieSecure`.

- [ ] **Step 3: Implement password bootstrap and configuration wiring.**

```go
rbacSubject := flag.String("rbac-bootstrap-subject", "", "Optional RBAC bootstrap user subject")
rbacPassword := flag.String("rbac-bootstrap-password", "", "Optional RBAC bootstrap user password")

func authCookieSecure() bool { return strings.ToLower(strings.TrimSpace(os.Getenv("AUTH_COOKIE_SECURE"))) != "false" }
```

When RBAC is enabled and both bootstrap fields are supplied, call `BootstrapAdmin(ctx, subject, password, 8*time.Hour)`. Pass `transport.SessionOptions{CookieName: "aicouncil_session", CookieSecure: authCookieSecure(), TTL: 8*time.Hour}` to the RBAC server constructor. Keep `--rbac-bootstrap-token` as a deprecated compatibility path for desktop/CLI bootstrap but document that browser login requires a password. Reject a password-only or subject-only bootstrap pair at startup.

- [ ] **Step 4: Document production operation.**

Add an executable startup example containing `--rbac-role=enabled --rbac-bootstrap-subject=admin --rbac-bootstrap-password='replace-me'`, explicitly state that `AUTH_COOKIE_SECURE=false` is local HTTP only, and include Caddy same-origin proxy configuration. Do not document or log plaintext generated access tokens.

- [ ] **Step 5: Run command and full Go tests.**

Run: `$env:GOCACHE='F:\AI-council\.tmp-go-cache'; go test ./cmd/council-server ./...`

Expected: all packages report `ok`.

- [ ] **Step 6: Commit deployment wiring.**

```powershell
git add cmd/council-server/main.go cmd/council-server/main_test.go README.md deploy/caddy/Caddyfile
git commit -m "feat: configure secure rbac web sessions"
```

### Task 4: Add typed same-origin authentication to the web client

**Files:**
- Modify: `web/lib/types.ts`
- Modify: `web/lib/api.ts`
- Modify: `web/lib/api.test.ts`
- Create: `web/components/auth-session.tsx`
- Create: `web/components/auth-session.test.tsx`

- [ ] **Step 1: Write API tests that assert browser credentials and desktop authorization coexist.**

```ts
it('sends same-origin cookies while retaining a desktop bearer token', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({data: []})))
  await listUsers()
  expect(fetch).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({credentials: 'same-origin'}))
})
```

- [ ] **Step 2: Run the focused test and verify it fails until credentials are added.**

Run: `& 'C:\Users\asus\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe' web\node_modules\vitest\vitest.mjs run --config web\vitest.config.ts web/lib/api.test.ts`

Expected: assertion failure for `credentials`.

- [ ] **Step 3: Implement public data types and API methods.**

```ts
export type Identity = {subject:string; roles:string[]; permissions:string[]}
export type User = Identity & {disabled:boolean}
export type Role = {name:string; permissions:string[]}

export function login(subject:string,password:string){return request<Identity>('/auth/login',{method:'POST',body:JSON.stringify({subject,password})})}
export function me(){return request<Identity>('/auth/me')}
export function logout(){return request<void>('/auth/logout',{method:'POST'})}
```

Change the shared request initializer to `{credentials:'same-origin', headers:{...authorizationHeader(), ...}}`, then add typed list/create/update methods for users, roles and permissions. Preserve the `authorizationHeader()` call exactly so desktop sessions still send their injected Bearer token.

- [ ] **Step 4: Implement the client session context and tests.**

```tsx
export function can(identity: Identity | undefined, permission: string) {
  return Boolean(identity?.permissions.some(value => value === permission || (value.endsWith(':*') && permission.startsWith(value.slice(0, -1)))))
}

export function AuthProvider({children}:{children:React.ReactNode}) {
  const [identity,setIdentity] = useState<Identity | undefined>()
  const [ready,setReady] = useState(false)
  useEffect(() => { me().then(setIdentity).catch(() => setIdentity(undefined)).finally(() => setReady(true)) }, [])
  return <AuthContext.Provider value={{identity,ready,refresh: async()=>setIdentity(await me())}}>{children}</AuthContext.Provider>
}
```

`RequirePermission` must render a named `role="alert"` access-denied state instead of its children when the session is ready but missing a permission; it must render a semantic loading state while `/auth/me` is pending.

- [ ] **Step 5: Run client API/session tests.**

Run: `& 'C:\Users\asus\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe' web\node_modules\vitest\vitest.mjs run --config web\vitest.config.ts web/lib/api.test.ts web/components/auth-session.test.tsx`

Expected: both test files pass.

- [ ] **Step 6: Commit the client auth foundation.**

```powershell
git add web/lib/types.ts web/lib/api.ts web/lib/api.test.ts web/components/auth-session.tsx web/components/auth-session.test.tsx
git commit -m "feat: add web rbac session client"
```

### Task 5: Deliver login, account, and user-management screens

**Files:**
- Modify: `web/app/layout.tsx`
- Create: `web/app/login/page.tsx`
- Create: `web/app/login/page.test.tsx`
- Create: `web/app/account/page.tsx`
- Create: `web/app/account/page.test.tsx`
- Create: `web/app/admin/users/page.tsx`
- Create: `web/app/admin/users/page.test.tsx`
- Modify: `web/app/globals.css`
- Modify: `web/app/globals.test.ts`

- [ ] **Step 1: Write screen tests before implementation.**

```tsx
it('submits credentials and redirects after login', async () => {
  render(<LoginPage />)
  await userEvent.type(screen.getByLabelText('账号'), 'admin')
  await userEvent.type(screen.getByLabelText('密码'), 'correct horse')
  await userEvent.click(screen.getByRole('button', {name:'登录'}))
  expect(login).toHaveBeenCalledWith('admin', 'correct horse')
})

it('does not render management forms without admin:users', () => {
  render(<UserManagementPage />)
  expect(screen.getByRole('alert')).toHaveTextContent('没有管理用户的权限')
})
```

- [ ] **Step 2: Run the new screen tests and verify they fail because pages do not exist.**

Run: `& 'C:\Users\asus\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe' web\node_modules\vitest\vitest.mjs run --config web\vitest.config.ts web/app/login/page.test.tsx web/app/account/page.test.tsx web/app/admin/users/page.test.tsx`

Expected: module-resolution failures for the new routes.

- [ ] **Step 3: Build the account-aware command-center shell.**

Wrap the existing layout main content with `AuthProvider`. While the session is ready, show `登录` for anonymous users or `账户` for authenticated users; show `用户管理` only if `can(identity, 'admin:users')`. Keep the existing task/model/workspace navigation and design tokens intact.

- [ ] **Step 4: Implement the three pages with deliberate states.**

```tsx
<form onSubmit={submit} className="auth-card">
  <label>账号<input name="subject" autoComplete="username" required /></label>
  <label>密码<input name="password" type="password" autoComplete="current-password" required /></label>
  {error && <p role="alert">{error}</p>}
  <button disabled={busy}>{busy ? '正在登录…' : '登录'}</button>
</form>
```

The account page displays subject, roles, and permissions and provides an explicit logout action. The admin screen has: a user table (subject, state, roles); a create-user form; an edit drawer/form for disabled state, password replacement and role multiselect; a role form that creates/updates its permission checklist; and clear empty/loading/error states. Password input values must be cleared after a successful request and never rendered in tables or logs.

- [ ] **Step 5: Add only necessary CSS.**

Add `.auth-card`, `.management-grid`, `.management-table`, `.status-chip`, and responsive single-column rules under the existing component styling section. Use current graphite surfaces, teal focus rings, and `prefers-reduced-motion`; do not introduce a second visual system or animation dependency.

- [ ] **Step 6: Run all frontend unit tests and static export.**

Run: `& 'C:\Users\asus\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe' web\node_modules\vitest\vitest.mjs run --config web\vitest.config.ts`

Expected: all Vitest suites pass.

Run: `& 'C:\Users\asus\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe' web\scripts\build-desktop.mjs`

Expected: Next static export completes successfully.

- [ ] **Step 7: Restore the tracked desktop placeholder after static build.**

Use `apply_patch` to restore `cmd/aicouncil-desktop/frontend/dist/index.html` to its tracked build-instruction placeholder, so generated export assets are not committed.

- [ ] **Step 8: Commit the web management UI.**

```powershell
git add web/app/layout.tsx web/app/login/page.tsx web/app/login/page.test.tsx web/app/account/page.tsx web/app/account/page.test.tsx web/app/admin/users/page.tsx web/app/admin/users/page.test.tsx web/app/globals.css web/app/globals.test.ts
git commit -m "feat: add web rbac account management"
```

### Task 6: Verify the complete browser and production build path

**Files:**
- Modify: `web/e2e/council.spec.ts`
- Modify: `README.md` (only if E2E setup details changed)

- [ ] **Step 1: Add the E2E contract for authenticated management.**

```ts
test('administrator can sign in and create a managed user', async ({page}) => {
  await page.goto('/login')
  await page.getByLabel('账号').fill('admin')
  await page.getByLabel('密码').fill('correct horse')
  await page.getByRole('button', {name:'登录'}).click()
  await page.getByRole('link', {name:'用户管理'}).click()
  await page.getByLabel('账号').last().fill('operator')
  await page.getByLabel('初始密码').fill('safe-passphrase')
  await page.getByRole('button', {name:'创建用户'}).click()
  await expect(page.getByText('operator')).toBeVisible()
})
```

Start Council in the test fixture with a temp SQLite database, `AUTH_COOKIE_SECURE=false`, a bootstrap password, and same-origin proxy/base URL. The fixture must delete only its own `t.TempDir()` path on cleanup and must not rely on a developer account or real provider key.

- [ ] **Step 2: Run the browser E2E test in a Chromium-capable environment.**

Run: `pnpm --dir web e2e`

Expected: the login and user creation scenario passes. If the current Windows host cannot start Chromium, record the exact launch failure and verify this command in Linux CI before release; do not weaken the browser assertions.

- [ ] **Step 3: Run final repository verification.**

Run: `$env:GOCACHE='F:\AI-council\.tmp-go-cache'; go test ./...`

Expected: all Go packages pass.

Run: `& 'C:\Users\asus\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe' web\node_modules\vitest\vitest.mjs run --config web\vitest.config.ts`

Expected: all frontend tests pass.

Run: `git diff --check; git status --short`

Expected: no whitespace errors and only intentional E2E/doc changes before commit.

- [ ] **Step 4: Commit verification coverage.**

```powershell
git add web/e2e/council.spec.ts README.md
git commit -m "test: cover web rbac management flow"
```

## Plan self-review

- Spec coverage: Tasks 1–2 implement password login, cookie/Bearer fallback, route-level permissions, admin APIs, rate limiting, and safe data projections. Task 3 makes secure cookie and bootstrap deployment explicit. Tasks 4–5 implement the same-origin browser client, account page, and permission-aware user/role interface. Task 6 validates browser, Go, frontend and desktop export paths.
- Completeness scan: every implementation step names its files, code shape, command, and expected result.
- Type consistency: `Identity`, `User`, `Role`, `SessionOptions`, `Login`, `UpdateUser`, `ReplaceRolePermissions`, and `can` are defined once and used with the same names throughout.
