package council

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/aicouncil/aicouncil/internal/app/group"
	storage "github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
)

// End-to-end handler test for Part B P6: seed an AI model directly in the
// store (profile + model + ready workspace), add it to a group, dispatch
// over HTTP, and drive the requirement endpoints. The proposal worker is
// not started in tests, so the requirement stays "dispatched" until it is
// cancelled — the pipeline itself is covered by service-level tests.
func TestGroupRequirementEndpointsEndToEnd(t *testing.T) {
	api, db := seedGroupsAPI(t)

	require.NoError(t, db.Create(&storage.ProviderProfileRecord{
		ID: "prof_1", OwnerUserID: "u_alice", Provider: "anthropic", Model: "claude-x-1",
	}).Error)
	require.NoError(t, db.Create(&storage.AIModelRecord{
		ID: "aim_1", ProviderProfileID: "prof_1", Model: "claude-x-1", Handle: "claude-x",
		DisplayName: "Claude X", Status: "active", CreatedBy: "u_alice",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}).Error)
	require.NoError(t, db.Create(&storage.ModelWorkspaceRecord{
		AIModelID: "aim_1", WorkspaceID: "workspace-aim-1", RunnerWorkspaceID: "workspace-aim-1",
		Status: "ready", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}).Error)

	code, body := callAIModel(t, api.createGroup, http.MethodPost, "/api/v1/groups",
		`{"name":"Team","members":[{"member_type":"user","user_id":"u_bob","group_role":"operator"},{"member_type":"ai","ai_model_id":"aim_1"}]}`, groupAlice, nil)
	require.Equal(t, http.StatusCreated, code, string(body))
	var created struct {
		Data group.GroupDetail `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &created))
	g := created.Data
	vars := map[string]string{"id": g.ID}

	// Empty list for members; 404 for non-members.
	code, body = callAIModel(t, api.listGroupRequirements, http.MethodGet, "/api/v1/groups/"+g.ID+"/requirements", "", groupAlice, vars)
	require.Equal(t, http.StatusOK, code, string(body))
	require.Contains(t, string(body), `"items":[]`)
	code, _ = callAIModel(t, api.listGroupRequirements, http.MethodGet, "/api/v1/groups/"+g.ID+"/requirements", "", groupEve, vars)
	require.Equal(t, http.StatusNotFound, code)

	// Dispatch over HTTP.
	code, body = callAIModel(t, api.postGroupMessage, http.MethodPost, "/api/v1/groups/"+g.ID+"/messages",
		`{"body_md":"@claude-x plan the migration","client_msg_id":"d1","intent":"dispatch"}`, groupAlice, vars)
	require.Equal(t, http.StatusCreated, code, string(body))

	code, body = callAIModel(t, api.listGroupRequirements, http.MethodGet, "/api/v1/groups/"+g.ID+"/requirements", "", groupAlice, vars)
	require.Equal(t, http.StatusOK, code, string(body))
	var page struct {
		Data group.RequirementPage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &page))
	require.Len(t, page.Data.Items, 1)
	req := page.Data.Items[0]
	require.Equal(t, group.ReqDispatched, req.State)
	require.Equal(t, "aim_1", req.AssigneeAIModelID)
	require.NotNil(t, req.Job)
	require.Equal(t, "queued", req.Job.State)

	rvars := map[string]string{"id": g.ID, "rid": req.ID}
	code, body = callAIModel(t, api.getGroupRequirement, http.MethodGet, "/api/v1/groups/"+g.ID+"/requirements/"+req.ID, "", groupBob, rvars)
	require.Equal(t, http.StatusOK, code, string(body))

	// Unknown requirement: 404 requirement_not_found.
	code, body = callAIModel(t, api.getGroupRequirement, http.MethodGet, "/api/v1/groups/"+g.ID+"/requirements/req_nope", "", groupAlice,
		map[string]string{"id": g.ID, "rid": "req_nope"})
	require.Equal(t, http.StatusNotFound, code)
	require.Contains(t, string(body), "requirement_not_found")

	// Retry from "dispatched" is a state conflict; cancel works; a second
	// cancel is idempotent (owner-decided 2026-11-25) and returns the
	// requirement as it stands.
	code, body = callAIModel(t, api.retryGroupRequirement, http.MethodPost, "/api/v1/groups/"+g.ID+"/requirements/"+req.ID+"/retry", "", groupAlice, rvars)
	require.Equal(t, http.StatusConflict, code, string(body))
	require.Contains(t, string(body), "requirement_state_conflict")
	code, body = callAIModel(t, api.cancelGroupRequirement, http.MethodPost, "/api/v1/groups/"+g.ID+"/requirements/"+req.ID+"/cancel",
		`{"reason":"changed my mind"}`, groupAlice, rvars)
	require.Equal(t, http.StatusOK, code, string(body))
	var cancelled struct {
		Data group.RequirementView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &cancelled))
	require.Equal(t, group.ReqCancelled, cancelled.Data.State)
	code, body = callAIModel(t, api.cancelGroupRequirement, http.MethodPost, "/api/v1/groups/"+g.ID+"/requirements/"+req.ID+"/cancel",
		`{"reason":"again"}`, groupAlice, rvars)
	require.Equal(t, http.StatusOK, code, string(body))
	require.NoError(t, json.Unmarshal(body, &cancelled))
	require.Equal(t, group.ReqCancelled, cancelled.Data.State)
}
