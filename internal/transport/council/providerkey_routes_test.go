package council

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	providerkeysvc "github.com/aicouncil/aicouncil/internal/security/providerkeys"
	"github.com/aicouncil/aicouncil/internal/security/rbac"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
)

func TestCreateProviderKeyRouteAcceptsBaseURLWithoutReturningSecret(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	keys, err := providerkeysvc.New(db, []byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	api := NewPersistentAPI(db).WithProviderKeys(keys)
	var handler http.HandlerFunc
	for _, route := range api.ProviderKeyRoutes() {
		if route.Method == http.MethodPost {
			handler = route.Handler
		}
	}
	require.NotNil(t, handler)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/keys", strings.NewReader(`{"provider":"openai","model":"openrouter/free","api_key":"router-secret","base_url":"https://openrouter.ai/api/v1/"}`))
	ctx := context.WithValue(req.Context(), identityContextKey{}, rbac.Identity{UserID: "owner-1"})
	recorder := httptest.NewRecorder()
	handler(recorder, req.WithContext(ctx))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	body, err := json.Marshal(response)
	require.NoError(t, err)
	require.Contains(t, string(body), "https://openrouter.ai/api/v1")
	require.NotContains(t, string(body), "router-secret")
}

func TestCreateProviderKeyRouteRejectsInsecureBaseURL(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	keys, err := providerkeysvc.New(db, []byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	api := NewPersistentAPI(db).WithProviderKeys(keys)
	var handler http.HandlerFunc
	for _, route := range api.ProviderKeyRoutes() {
		if route.Method == http.MethodPost {
			handler = route.Handler
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/keys", strings.NewReader(`{"provider":"openai","model":"router/model","api_key":"router-secret","base_url":"http://router.example/v1"}`))
	req = req.WithContext(context.WithValue(req.Context(), identityContextKey{}, rbac.Identity{UserID: "owner-1"}))
	recorder := httptest.NewRecorder()
	handler(recorder, req)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}
