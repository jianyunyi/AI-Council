package council

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/aicouncil/aicouncil/internal/app/aimodel"
	"github.com/aicouncil/aicouncil/internal/security/providerkeys"
	"github.com/aicouncil/aicouncil/internal/security/rbac"
	storage "github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/rest/pathvar"
)

type aiModelEnvelope struct {
	Data  aimodel.View `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func seedAIModelsAPI(t *testing.T) (*API, providerkeys.Profile) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "aimodels.db"))
	require.NoError(t, err)
	keyService, err := providerkeys.New(db, bytes.Repeat([]byte{9}, 32))
	require.NoError(t, err)
	profile, err := keyService.Create(context.Background(), "alice", "openai", "gpt-4o", "sk-test-key")
	require.NoError(t, err)
	api := NewPersistentAPI(db)
	require.NotNil(t, InstallAIModels(api))
	return api, profile
}

var (
	aiAdmin  = &rbac.Identity{UserID: "alice", Permissions: []string{"ai_model:manage"}}
	aiReader = &rbac.Identity{UserID: "bob", Permissions: []string{"provider:read"}}
	aiWSRead = &rbac.Identity{UserID: "carol", Permissions: []string{"model_workspace:read"}}
	aiNobody = &rbac.Identity{UserID: "dave", Permissions: []string{}}
)

func callAIModel(t *testing.T, handler http.HandlerFunc, method, target, body string, identity *rbac.Identity, vars map[string]string) (int, []byte) {
	t.Helper()
	var reader *bytes.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	if vars != nil {
		req = pathvar.WithVars(req, vars)
	}
	if identity != nil {
		req = req.WithContext(context.WithValue(req.Context(), identityContextKey{}, *identity))
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func TestAIModelEndpointsCreateListAndPermissions(t *testing.T) {
	api, profile := seedAIModelsAPI(t)

	// Create requires ai_model:manage.
	code, raw := callAIModel(t, api.createAIModel, http.MethodPost, "/api/v1/ai-models",
		`{"provider_profile_id":"`+profile.ID+`","model":"gpt-4o","handle":"GPT-4O"}`, aiReader, nil)
	require.Equal(t, http.StatusForbidden, code, string(raw))

	code, raw = callAIModel(t, api.createAIModel, http.MethodPost, "/api/v1/ai-models",
		`{"provider_profile_id":"`+profile.ID+`","model":"gpt-4o","handle":"GPT-4O"}`, aiAdmin, nil)
	require.Equal(t, http.StatusCreated, code, string(raw))
	require.NotContains(t, string(raw), "sk-test-key")
	var created aiModelEnvelope
	require.NoError(t, json.Unmarshal(raw, &created))
	require.Equal(t, "gpt-4o", created.Data.Handle)
	require.NotNil(t, created.Data.Workspace)
	require.Equal(t, "model_private", created.Data.Workspace.Kind)

	// The backing workspace record carries the model_private kind.
	var wsRecord storage.WorkspaceRecord
	require.NoError(t, api.db.Where("id = ?", created.Data.Workspace.WorkspaceID).First(&wsRecord).Error)
	require.Equal(t, "model_private", wsRecord.Kind)
	require.Equal(t, "models/gpt-4o", wsRecord.RunnerPath)

	// Validation errors surface with their stable codes.
	code, raw = callAIModel(t, api.createAIModel, http.MethodPost, "/api/v1/ai-models",
		`{"provider_profile_id":"missing","model":"gpt-4o","handle":"other"}`, aiAdmin, nil)
	require.Equal(t, http.StatusUnprocessableEntity, code, string(raw))
	require.Contains(t, string(raw), "profile_not_found")
	code, raw = callAIModel(t, api.createAIModel, http.MethodPost, "/api/v1/ai-models",
		`{"provider_profile_id":"`+profile.ID+`","model":"gpt-unknown","handle":"other"}`, aiAdmin, nil)
	require.Equal(t, http.StatusUnprocessableEntity, code, string(raw))
	require.Contains(t, string(raw), "model_not_offered")

	// List is readable with provider:read, forbidden with no permissions.
	code, raw = callAIModel(t, api.listAIModels, http.MethodGet, "/api/v1/ai-models", "", aiReader, nil)
	require.Equal(t, http.StatusOK, code, string(raw))
	require.Contains(t, string(raw), "gpt-4o")
	code, _ = callAIModel(t, api.listAIModels, http.MethodGet, "/api/v1/ai-models", "", aiNobody, nil)
	require.Equal(t, http.StatusForbidden, code)

	// Workspace view requires model_workspace:read (or manage).
	vars := map[string]string{"id": created.Data.ID}
	code, _ = callAIModel(t, api.getAIModelWorkspace, http.MethodGet, "/api/v1/ai-models/"+created.Data.ID+"/workspace", "", aiReader, vars)
	require.Equal(t, http.StatusForbidden, code)
	code, raw = callAIModel(t, api.getAIModelWorkspace, http.MethodGet, "/api/v1/ai-models/"+created.Data.ID+"/workspace", "", aiWSRead, vars)
	require.Equal(t, http.StatusOK, code, string(raw))
	require.Contains(t, string(raw), created.Data.Workspace.WorkspaceID)

	// Retry requires ai_model:manage (state-dependent retry semantics are
	// covered by the aimodel service tests; the async provisioner makes the
	// workspace status here timing-dependent).
	code, _ = callAIModel(t, api.retryAIModelWorkspace, http.MethodPost, "/api/v1/ai-models/"+created.Data.ID+"/workspace/retry", "", aiReader, vars)
	require.Equal(t, http.StatusForbidden, code)
}

func TestAIModelEndpointsUnavailableWithoutInstall(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "bare.db"))
	require.NoError(t, err)
	api := NewPersistentAPI(db) // InstallAIModels not called
	code, raw := callAIModel(t, api.listAIModels, http.MethodGet, "/api/v1/ai-models", "", aiAdmin, nil)
	require.Equal(t, http.StatusServiceUnavailable, code, string(raw))
	require.Contains(t, string(raw), "ai_models_unavailable")
}
