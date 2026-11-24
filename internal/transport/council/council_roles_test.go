package council

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	appTask "github.com/aicouncil/aicouncil/internal/app/task"
	councilengine "github.com/aicouncil/aicouncil/internal/council"
	"github.com/aicouncil/aicouncil/internal/council/roles"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/provider"
	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	"github.com/aicouncil/aicouncil/internal/security/providerkeys"
	"github.com/aicouncil/aicouncil/internal/security/rbac"
	storage "github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/rest/pathvar"
	"google.golang.org/grpc"
)

type rolesEnvelope struct {
	Data  councilRolesResponse `json:"data"`
	Error *struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Issues  []rolesIssueDTO `json:"issues"`
	} `json:"error"`
}

type rolesIssueDTO struct {
	Code string `json:"code"`
	Role string `json:"role"`
}

func seedCouncilRolesAPI(t *testing.T) (*API, providerkeys.Profile, providerkeys.Profile) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "roles.db"))
	require.NoError(t, err)
	keyService, err := providerkeys.New(db, bytes.Repeat([]byte{7}, 32))
	require.NoError(t, err)
	ctx := context.Background()
	openai, err := keyService.Create(ctx, "alice", "openai", "gpt-4o", "sk-openai-test")
	require.NoError(t, err)
	deepseek, err := keyService.Create(ctx, "alice", "deepseek", "deepseek-chat", "sk-deepseek-test")
	require.NoError(t, err)
	api := NewPersistentAPI(db).WithProviderKeys(keyService).WithCouncilRoles(storage.NewCouncilRolesRepository(db))
	return api, openai, deepseek
}

var aliceAdmin = &rbac.Identity{UserID: "alice", Permissions: []string{"council:read", "council:configure"}}

func callRoles(t *testing.T, api *API, method, target, body string, identity *rbac.Identity, headers map[string]string) (int, []byte) {
	t.Helper()
	var reader *bytes.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if identity != nil {
		req = req.WithContext(context.WithValue(req.Context(), identityContextKey{}, *identity))
	}
	rec := httptest.NewRecorder()
	switch method {
	case http.MethodGet:
		api.getCouncilRoles(rec, req)
	case http.MethodPut:
		api.putCouncilRoles(rec, req)
	case http.MethodDelete:
		api.deleteCouncilRoles(rec, req)
	case http.MethodPost:
		api.validateCouncilRoles(rec, req)
	}
	return rec.Code, rec.Body.Bytes()
}

func decodeRoles(t *testing.T, raw []byte) rolesEnvelope {
	t.Helper()
	var envelope rolesEnvelope
	require.NoError(t, json.Unmarshal(raw, &envelope))
	return envelope
}

func validSlotsJSON(openaiID, deepseekID string) string {
	return `{"scope_type":"global","slots":[` +
		`{"role":"proposer","ordinal":0,"provider_profile_id":"` + openaiID + `","model":"gpt-4o","enabled":true},` +
		`{"role":"judge","ordinal":0,"provider_profile_id":"` + deepseekID + `","model":"deepseek-chat","enabled":true}]}`
}

func TestCouncilRolesPutGetVersionConflictAndValidation(t *testing.T) {
	api, openai, deepseek := seedCouncilRolesAPI(t)

	// Initially: no stored set, legacy preview built from the profiles.
	code, raw := callRoles(t, api, http.MethodGet, "/api/v1/council/roles", "", aliceAdmin, nil)
	require.Equal(t, http.StatusOK, code, string(raw))
	view := decodeRoles(t, raw)
	require.Equal(t, 0, view.Data.Version)
	require.Equal(t, "legacy", view.Data.EffectiveSource)
	require.Len(t, view.Data.ResolvedPreview.Proposers, 2)
	require.NotContains(t, string(raw), "sk-openai-test", "responses never include keys")

	// Create the global set (implicit version 0).
	code, raw = callRoles(t, api, http.MethodPut, "/api/v1/council/roles", validSlotsJSON(openai.ID, deepseek.ID), aliceAdmin, nil)
	require.Equal(t, http.StatusOK, code, string(raw))
	view = decodeRoles(t, raw)
	require.Equal(t, 1, view.Data.Version)
	require.Equal(t, "global", view.Data.EffectiveSource)
	require.Len(t, view.Data.Roles, 2)
	require.Len(t, view.Data.ResolvedPreview.Proposers, 1)

	// A stale writer conflicts.
	code, raw = callRoles(t, api, http.MethodPut, "/api/v1/council/roles", validSlotsJSON(openai.ID, deepseek.ID), aliceAdmin, nil)
	require.Equal(t, http.StatusConflict, code, string(raw))
	require.Equal(t, "version_conflict", decodeRoles(t, raw).Error.Code)

	// If-Match with the current version succeeds.
	code, raw = callRoles(t, api, http.MethodPut, "/api/v1/council/roles", validSlotsJSON(openai.ID, deepseek.ID), aliceAdmin, map[string]string{"If-Match": "1"})
	require.Equal(t, http.StatusOK, code, string(raw))
	require.Equal(t, 2, decodeRoles(t, raw).Data.Version)

	// Validation failures are 422 with codes: wrong model + missing judge.
	bad := `{"scope_type":"global","slots":[{"role":"proposer","ordinal":0,"provider_profile_id":"` + openai.ID + `","model":"gpt-nope","enabled":true}]}`
	code, raw = callRoles(t, api, http.MethodPut, "/api/v1/council/roles", bad, aliceAdmin, map[string]string{"If-Match": "2"})
	require.Equal(t, http.StatusUnprocessableEntity, code, string(raw))
	codes := map[string]bool{}
	for _, issue := range decodeRoles(t, raw).Error.Issues {
		codes[issue.Code] = true
	}
	require.True(t, codes["model_not_offered"], "issues: %s", raw)
	require.True(t, codes["cardinality"], "issues: %s", raw)

	// Dry-run validate reports the same codes without saving.
	code, raw = callRoles(t, api, http.MethodPost, "/api/v1/council/roles/validate", bad, aliceAdmin, nil)
	require.Equal(t, http.StatusOK, code, string(raw))
	require.Contains(t, string(raw), `"valid":false`)
	require.Contains(t, string(raw), "model_not_offered")
}

func TestCouncilRolesWorkspaceScopeFallbackAndDelete(t *testing.T) {
	api, openai, deepseek := seedCouncilRolesAPI(t)
	code, _ := callRoles(t, api, http.MethodPut, "/api/v1/council/roles", validSlotsJSON(openai.ID, deepseek.ID), aliceAdmin, nil)
	require.Equal(t, http.StatusOK, code)

	workspaceSlots := `{"scope_type":"workspace","scope_id":"ws-1","slots":[` +
		`{"role":"proposer","ordinal":0,"provider_profile_id":"` + deepseek.ID + `","model":"deepseek-chat","enabled":true},` +
		`{"role":"judge","ordinal":0,"provider_profile_id":"` + deepseek.ID + `","model":"deepseek-chat","enabled":true}]}`
	code, raw := callRoles(t, api, http.MethodPut, "/api/v1/council/roles", workspaceSlots, aliceAdmin, nil)
	require.Equal(t, http.StatusOK, code, string(raw))
	view := decodeRoles(t, raw)
	require.Equal(t, "workspace", view.Data.EffectiveSource)
	require.Equal(t, deepseek.ID, view.Data.ResolvedPreview.Proposers[0].ProviderProfileID)

	// Deleting the workspace set reverts resolution to the global set.
	code, raw = callRoles(t, api, http.MethodDelete, "/api/v1/council/roles?scope_type=workspace&scope_id=ws-1", "", aliceAdmin, nil)
	require.Equal(t, http.StatusOK, code, string(raw))
	view = decodeRoles(t, raw)
	require.Equal(t, "global", view.Data.EffectiveSource)
	require.Empty(t, view.Data.Roles)

	// The global set cannot be deleted.
	code, _ = callRoles(t, api, http.MethodDelete, "/api/v1/council/roles?scope_type=global", "", aliceAdmin, nil)
	require.Equal(t, http.StatusBadRequest, code)
}

func TestCouncilRolesPermissions(t *testing.T) {
	api, openai, deepseek := seedCouncilRolesAPI(t)
	// An identity without council permissions and without workspace access
	// is denied reads and writes on the global scope.
	stranger := &rbac.Identity{UserID: "mallory", Permissions: []string{"task:read"}}
	code, _ := callRoles(t, api, http.MethodGet, "/api/v1/council/roles", "", stranger, nil)
	require.Equal(t, http.StatusForbidden, code)
	code, _ = callRoles(t, api, http.MethodPut, "/api/v1/council/roles", validSlotsJSON(openai.ID, deepseek.ID), stranger, nil)
	require.Equal(t, http.StatusForbidden, code)

	// council:read can read but not write.
	reader := &rbac.Identity{UserID: "mallory", Permissions: []string{"council:read"}}
	code, _ = callRoles(t, api, http.MethodGet, "/api/v1/council/roles", "", reader, nil)
	require.Equal(t, http.StatusOK, code)
	code, _ = callRoles(t, api, http.MethodPut, "/api/v1/council/roles", validSlotsJSON(openai.ID, deepseek.ID), reader, nil)
	require.Equal(t, http.StatusForbidden, code)
}

func TestListProviderModels(t *testing.T) {
	api, openai, _ := seedCouncilRolesAPI(t)
	req := pathvar.WithVars(httptest.NewRequest(http.MethodGet, "/api/v1/providers/"+openai.ID+"/models", nil), map[string]string{"id": openai.ID})
	req = req.WithContext(context.WithValue(req.Context(), identityContextKey{}, *aliceAdmin))
	rec := httptest.NewRecorder()
	api.listProviderModels(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "gpt-4o")
	require.NotContains(t, rec.Body.String(), "sk-openai-test")

	req = pathvar.WithVars(httptest.NewRequest(http.MethodGet, "/api/v1/providers/missing/models", nil), map[string]string{"id": "missing"})
	req = req.WithContext(context.WithValue(req.Context(), identityContextKey{}, *aliceAdmin))
	rec = httptest.NewRecorder()
	api.listProviderModels(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

type stubStageProvider struct {
	name string
	fail bool
}

func (p stubStageProvider) Name() string { return p.name }
func (p stubStageProvider) Generate(_ context.Context, req provider.Request) (provider.Response, error) {
	if p.fail {
		return provider.Response{}, errors.New("stub provider down")
	}
	content := req.Messages[len(req.Messages)-1].Content
	var value any
	switch {
	case bytes.Contains([]byte(content), []byte("Select the best proposals")):
		value = schema.CouncilDecision{Plan: schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "go", Args: []string{"test", "./..."}, TimeoutSeconds: 30}}}}
	case bytes.Contains([]byte(content), []byte("Find blocking risks")):
		value = schema.RedTeamReport{}
	case bytes.Contains([]byte(content), []byte("Review the anonymous proposal")):
		value = schema.PeerReview{Verdict: "approve"}
	default:
		value = schema.Proposal{ID: "p1"}
	}
	raw, _ := json.Marshal(value)
	return provider.Response{Content: raw}, nil
}

type stubRunner struct{}

func (stubRunner) ExecuteApprovedPlan(context.Context, *runnerv1.ExecuteApprovedPlanRequest, ...grpc.CallOption) (*runnerv1.ExecuteApprovedPlanResponse, error) {
	return &runnerv1.ExecuteApprovedPlanResponse{}, nil
}
func (stubRunner) ReadWorkspaceContext(context.Context, *runnerv1.ReadWorkspaceContextRequest, ...grpc.CallOption) (*runnerv1.ReadWorkspaceContextResponse, error) {
	return &runnerv1.ReadWorkspaceContextResponse{}, nil
}

func TestStartTaskSavesCouncilSnapshotAndSlotSkippedEvents(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "start.db"))
	require.NoError(t, err)
	require.NoError(t, db.Create(&storage.WorkspaceRecord{ID: "ws-1", OwnerUserID: "alice"}).Error)
	require.NoError(t, db.Create(&storage.TaskRecord{ID: "task-1", OwnerUserID: "alice", CreatedBy: "alice", WorkspaceID: "ws-1", Requirement: "do it", State: "DRAFT", PlanVersion: 1}).Error)

	resolved := roles.Resolved{
		Source:  roles.SourceGlobal,
		Version: 2,
		Proposers: []roles.ResolvedSlot{
			{Role: roles.RoleProposer, Ordinal: 0, ProviderProfileID: "prof-good", Provider: "openai", Model: "gpt-4o"},
			{Role: roles.RoleProposer, Ordinal: 1, ProviderProfileID: "prof-bad", Provider: "openai", Model: "gpt-4o"},
		},
		Judge: []roles.ResolvedSlot{
			{Role: roles.RoleJudge, Ordinal: 0, ProviderProfileID: "prof-good", Provider: "openai", Model: "gpt-4o"},
		},
	}
	api := NewPersistentAPI(db).
		WithCouncilRoles(storage.NewCouncilRolesRepository(db)).
		WithRunnerClient(stubRunner{}).
		WithCouncilFactory(func(context.Context, string, string) (appTask.CouncilPort, error) {
			return councilengine.WorkflowFromResolved(resolved, func(slot roles.ResolvedSlot) (provider.ModelProvider, error) {
				return stubStageProvider{name: slot.ProviderProfileID, fail: slot.ProviderProfileID == "prof-bad"}, nil
			})
		})

	req := pathvar.WithVars(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-1/start", nil), map[string]string{"id": "task-1"})
	req = req.WithContext(context.WithValue(req.Context(), identityContextKey{}, *aliceAdmin))
	rec := httptest.NewRecorder()
	api.startTask(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var run storage.RunRecord
	require.NoError(t, db.Where("id = ?", "task-1").First(&run).Error)
	require.Contains(t, string(run.CouncilConfigJSON), `"source":"global"`)
	require.Contains(t, string(run.CouncilConfigJSON), "prof-good")

	started := api.tasks["task-1"]
	require.NotNil(t, started)
	foundSkip := false
	for _, event := range started.Events {
		if event.Type == "council.slot_skipped" {
			foundSkip = true
		}
	}
	require.True(t, foundSkip, "expected a council.slot_skipped event for the failing proposer")
}
