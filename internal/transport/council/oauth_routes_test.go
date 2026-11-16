package council

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	oauthsvc "github.com/aicouncil/aicouncil/internal/security/oauth"
	"github.com/aicouncil/aicouncil/internal/security/rbac"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/rest/pathvar"
)

func TestOAuthStartCallbackSetsSessionAndConsumesState(t *testing.T) {
	provider := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"provider-token"}`))
		case "/user":
			_, _ = w.Write([]byte(`{"id":77}`))
		case "/user/emails":
			_, _ = w.Write([]byte(`[{"email":"oauth@example.com","primary":true,"verified":true}]`))
		default:
			http.NotFound(w, r)
		}
	})
	client := &http.Client{Transport: localOAuthRoundTripper{handler: provider}}
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "oauth-http.db"))
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, rbac.New(db).SeedPermissions(context.Background()))
	const providerURL = "http://127.0.0.1"
	oauth := oauthsvc.New(db, map[string]oauthsvc.Config{"github": {ClientID: "id", ClientSecret: "secret", RedirectURL: "https://app.example/api/v1/auth/oauth/github/callback", AuthURL: "https://github.com/login/oauth/authorize", TokenURL: providerURL + "/token", APIBaseURL: providerURL}}, client)
	api := NewRBACAPIWithAuthFlows(rbac.New(db), SessionOptions{CookieSecure: true, TTL: time.Hour}, nil, nil, oauth)
	start := routeHandler(t, api.Routes(), http.MethodGet, "/api/v1/auth/oauth/:provider/start")
	startReq := pathvar.WithVars(httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/github/start", nil), map[string]string{"provider": "github"})
	startRec := httptest.NewRecorder()
	RBACAuth(rbac.New(db), "")(start)(startRec, startReq)
	require.Equal(t, http.StatusFound, startRec.Code)
	stateCookie := startRec.Result().Cookies()[0]
	parsed, err := url.Parse(startRec.Header().Get("Location"))
	require.NoError(t, err)
	state := parsed.Query().Get("state")
	callback := routeHandler(t, api.Routes(), http.MethodGet, "/api/v1/auth/oauth/:provider/callback")
	callbackReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/github/callback?code=auth-code&state="+url.QueryEscape(state), nil)
	callbackReq = pathvar.WithVars(callbackReq, map[string]string{"provider": "github"})
	callbackReq.AddCookie(stateCookie)
	callbackRec := httptest.NewRecorder()
	RBACAuth(rbac.New(db), "")(callback)(callbackRec, callbackReq)
	require.Equal(t, http.StatusSeeOther, callbackRec.Code)
	require.Equal(t, "/account", callbackRec.Header().Get("Location"))
	var session *http.Cookie
	for _, cookie := range callbackRec.Result().Cookies() {
		if cookie.Name == "aicouncil_session" {
			session = cookie
		}
	}
	require.NotNil(t, session)
	require.True(t, session.HttpOnly)
	identity, err := rbac.New(db).Authenticate(context.Background(), session.Value)
	require.NoError(t, err)
	require.Equal(t, "oauth@example.com", identity.Subject)
	require.True(t, isPublicRBACPath(http.MethodGet, "/api/v1/auth/oauth/github/start"))
	require.False(t, isPublicRBACPath(http.MethodGet, "/api/v1/auth/oauth/github/admin"))
}

type localOAuthRoundTripper struct{ handler http.Handler }

func (rt localOAuthRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	response := httptest.NewRecorder()
	rt.handler.ServeHTTP(response, request)
	result := response.Result()
	result.Body = io.NopCloser(bytes.NewReader(response.Body.Bytes()))
	result.Request = request
	return result, nil
}
