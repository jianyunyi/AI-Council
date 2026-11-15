package council

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/aicouncil/aicouncil/internal/security/rbac"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/rest/pathvar"
)

func withUser(request *http.Request, userID string) *http.Request {
	return request.WithContext(context.WithValue(request.Context(), identityContextKey{}, rbac.Identity{UserID: userID}))
}

func TestWorkspaceAndTaskResourcesAreScopedToAuthenticatedOwner(t *testing.T) {
	api := NewAPI().WithRunnerClient(&routeRunner{})
	createWorkspace := routeHandler(t, api.Routes(), http.MethodPost, "/api/v1/workspaces")
	createTask := routeHandler(t, api.Routes(), http.MethodPost, "/api/v1/tasks")
	listWorkspaces := routeHandler(t, api.Routes(), http.MethodGet, "/api/v1/workspaces")
	getTask := routeHandler(t, api.Routes(), http.MethodGet, "/api/v1/tasks/:id")
	request := func(handler http.HandlerFunc, method, path, body, user string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req = withUser(req, user)
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}
	wsRec := request(createWorkspace, http.MethodPost, "/api/v1/workspaces", `{"root":"alice/repo"}`, "alice")
	require.Equal(t, http.StatusCreated, wsRec.Code)
	wsID := `"id":"workspace-1"`
	require.Contains(t, wsRec.Body.String(), wsID)
	taskRec := request(createTask, http.MethodPost, "/api/v1/tasks", `{"workspace_id":"workspace-1","requirement":"private task","acceptance":["done"]}`, "alice")
	require.Equal(t, http.StatusCreated, taskRec.Code)
	require.Contains(t, taskRec.Body.String(), `"id":"task-2"`)

	list := request(listWorkspaces, http.MethodGet, "/api/v1/workspaces", "", "bob")
	require.Equal(t, http.StatusOK, list.Code)
	require.NotContains(t, list.Body.String(), "workspace-1")
	createForeignTask := request(createTask, http.MethodPost, "/api/v1/tasks", `{"workspace_id":"workspace-1","requirement":"intrusion","acceptance":["done"]}`, "bob")
	require.Equal(t, http.StatusBadRequest, createForeignTask.Code)
	foreignGet := pathvar.WithVars(withUser(httptest.NewRequest(http.MethodGet, "/api/v1/tasks/task-2", nil), "bob"), map[string]string{"id": "task-2"})
	foreignRec := httptest.NewRecorder()
	getTask(foreignRec, foreignGet)
	require.Equal(t, http.StatusNotFound, foreignRec.Code)
}

func TestWorkspaceShareControlsDetailAndOwnerOnlyManagement(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "shares.db"))
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	api := NewPersistentAPI(db)
	require.NoError(t, db.Create(&sqlite.UserRecord{ID: "owner-id", Subject: "owner"}).Error)
	require.NoError(t, db.Create(&sqlite.UserRecord{ID: "editor-id", Subject: "editor"}).Error)
	require.NoError(t, db.Create(&sqlite.UserRecord{ID: "viewer-id", Subject: "viewer"}).Error)
	require.NoError(t, db.Create(&sqlite.WorkspaceRecord{ID: "ws", OwnerUserID: "owner-id", CanonicalRoot: "/workspace/repo", RunnerPath: "repo"}).Error)
	api.workspaces["ws"] = workspace{ID: "ws", OwnerUserID: "owner-id", Root: "/workspace/repo", RunnerPath: "repo"}
	shares := api.WorkspaceShareRoutes()
	put := routeHandler(t, shares, http.MethodPut, "/api/v1/workspaces/:id/shares/:subject")
	remove := routeHandler(t, shares, http.MethodDelete, "/api/v1/workspaces/:id/shares/:subject")
	detail := routeHandler(t, api.Routes(), http.MethodGet, "/api/v1/workspaces/:id")
	request := func(handler http.HandlerFunc, method, path, body, user string, vars map[string]string) *httptest.ResponseRecorder {
		req := withUser(httptest.NewRequest(method, path, bytes.NewBufferString(body)), user)
		req = pathvar.WithVars(req, vars)
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}
	ownerID := map[string]string{"id": "ws", "subject": "viewer"}
	require.Equal(t, http.StatusNoContent, request(put, http.MethodPut, "/api/v1/workspaces/ws/shares/viewer", `{"access":"viewer"}`, "owner-id", ownerID).Code)
	require.True(t, api.canReadWorkspace(context.Background(), "ws", "viewer-id"))
	require.False(t, api.canEditWorkspace(context.Background(), "ws", "viewer-id"))
	require.Equal(t, http.StatusOK, request(detail, http.MethodGet, "/api/v1/workspaces/ws", "", "viewer-id", map[string]string{"id": "ws"}).Code)
	deniedGrant := request(put, http.MethodPut, "/api/v1/workspaces/ws/shares/editor", `{"access":"editor"}`, "viewer-id", map[string]string{"id": "ws", "subject": "editor"})
	require.Equal(t, http.StatusNotFound, deniedGrant.Code)
	require.Equal(t, http.StatusNoContent, request(put, http.MethodPut, "/api/v1/workspaces/ws/shares/editor", `{"access":"editor"}`, "owner-id", map[string]string{"id": "ws", "subject": "editor"}).Code)
	require.True(t, api.canEditWorkspace(context.Background(), "ws", "editor-id"))
	require.Equal(t, http.StatusNoContent, request(remove, http.MethodDelete, "/api/v1/workspaces/ws/shares/viewer", "", "owner-id", ownerID).Code)
	require.False(t, api.canReadWorkspace(context.Background(), "ws", "viewer-id"))
	require.Equal(t, http.StatusNotFound, request(detail, http.MethodGet, "/api/v1/workspaces/ws", "", "viewer-id", map[string]string{"id": "ws"}).Code)
}
