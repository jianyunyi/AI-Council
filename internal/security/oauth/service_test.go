package oauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aicouncil/aicouncil/internal/security/rbac"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
)

func TestStartAndCompleteGitHubOAuthCreatesVerifiedMember(t *testing.T) {
	client := oauthTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"github-access","token_type":"bearer"}`))
		case "/user":
			_, _ = w.Write([]byte(`{"id":42}`))
		case "/user/emails":
			_, _ = w.Write([]byte(`[{"email":"member@example.com","primary":true,"verified":true}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	const providerURL = "http://127.0.0.1"
	svc := newTestOAuthService(t, map[string]Config{"github": {ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app.example/api/v1/auth/oauth/github/callback", AuthURL: "https://github.com/login/oauth/authorize", TokenURL: providerURL + "/token", APIBaseURL: providerURL}}, client)
	flow, err := svc.Start(context.Background(), "github")
	require.NoError(t, err)
	require.Contains(t, flow.URL, "code_challenge_method=S256")
	require.Contains(t, flow.URL, flow.State)
	result, err := svc.Complete(context.Background(), "github", "authorization-code", flow.State, flow.Verifier, flow.Nonce, time.Hour)
	require.NoError(t, err)
	require.NotEmpty(t, result.AccessToken)
	require.Equal(t, "member@example.com", result.Identity.Subject)
	require.Contains(t, result.Identity.Roles, "member")
	_, err = svc.Complete(context.Background(), "github", "authorization-code", flow.State, flow.Verifier, flow.Nonce, time.Hour)
	require.ErrorIs(t, err, ErrInvalidFlow)
}

func TestOAuthStateIsBoundToProviderAndVerifier(t *testing.T) {
	client := oauthTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"token"}`))
		case "/user":
			_, _ = w.Write([]byte(`{"id":17}`))
		case "/user/emails":
			_, _ = w.Write([]byte(`[{"email":"verified@example.com","primary":true,"verified":true}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	const providerURL = "http://127.0.0.1"
	svc := newTestOAuthService(t, map[string]Config{
		"github": {ClientID: "g", ClientSecret: "s", RedirectURL: "https://app.example/cb", AuthURL: "https://github.com/login/oauth/authorize", TokenURL: providerURL + "/token", APIBaseURL: providerURL},
		"google": {ClientID: "x", ClientSecret: "y", RedirectURL: "https://app.example/cb", AuthURL: "https://accounts.google.com/o/oauth2/v2/auth"},
	}, client)
	flow, err := svc.Start(context.Background(), "github")
	require.NoError(t, err)
	_, err = svc.Complete(context.Background(), "google", "code", flow.State, flow.Verifier, flow.Nonce, time.Hour)
	require.ErrorIs(t, err, ErrInvalidFlow)
	_, err = svc.Complete(context.Background(), "github", "code", flow.State, strings.Repeat("x", 48), flow.Nonce, time.Hour)
	require.ErrorIs(t, err, ErrInvalidFlow)
	result, err := svc.Complete(context.Background(), "github", "code", flow.State, flow.Verifier, flow.Nonce, time.Hour)
	require.NoError(t, err)
	require.Equal(t, "verified@example.com", result.Identity.Subject)
}

func TestGoogleOAuthRequiresVerifiedSignedIDTokenWithMatchingAudienceAndNonce(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	var nonce string
	client := oauthTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			claims := map[string]any{"iss": "https://accounts.google.com", "sub": "google-subject", "aud": "google-client", "email": "google@example.com", "email_verified": true, "nonce": nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix()}
			token := signTestJWT(t, privateKey, claims)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"access","id_token":"` + token + `"}`))
		case "/jwks":
			e := big.NewInt(int64(privateKey.PublicKey.E)).Bytes()
			payload, _ := json.Marshal(map[string]any{"keys": []map[string]string{{"kid": "key-1", "kty": "RSA", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(e)}}})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	const providerURL = "http://127.0.0.1"
	svc := newTestOAuthService(t, map[string]Config{"google": {ClientID: "google-client", ClientSecret: "secret", RedirectURL: "https://app.example/callback", AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: providerURL + "/token", JWKSURL: providerURL + "/jwks"}}, client)
	flow, err := svc.Start(context.Background(), "google")
	require.NoError(t, err)
	nonce = flow.Nonce
	result, err := svc.Complete(context.Background(), "google", "code", flow.State, flow.Verifier, flow.Nonce, time.Hour)
	require.NoError(t, err)
	require.Equal(t, "google@example.com", result.Identity.Subject)
}

func TestVerifiedEmailLinksMultipleProvidersToOneLocalUser(t *testing.T) {
	svc := newTestOAuthService(t, map[string]Config{})
	first, err := svc.linkIdentity(context.Background(), "github", "101", "same@example.com")
	require.NoError(t, err)
	second, err := svc.linkIdentity(context.Background(), "google", "google-sub", "same@example.com")
	require.NoError(t, err)
	require.Equal(t, first, second)
}

func signTestJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	encode := func(value any) string {
		data, err := json.Marshal(value)
		require.NoError(t, err)
		return base64.RawURLEncoding.EncodeToString(data)
	}
	message := encode(map[string]string{"alg": "RS256", "kid": "key-1", "typ": "JWT"}) + "." + encode(claims)
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	require.NoError(t, err)
	return message + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func oauthTestClient(handler http.Handler) *http.Client {
	return &http.Client{Transport: oauthTestRoundTripper(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, r)
		return recorder.Result(), nil
	})}
}

type oauthTestRoundTripper func(*http.Request) (*http.Response, error)

func (f oauthTestRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newTestOAuthService(t *testing.T, configs map[string]Config, clients ...*http.Client) *Service {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "oauth.db"))
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, rbac.New(db).SeedPermissions(context.Background()))
	var client *http.Client
	if len(clients) != 0 {
		client = clients[0]
	}
	return New(db, configs, client)
}
