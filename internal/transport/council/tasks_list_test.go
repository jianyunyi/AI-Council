package council

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/aicouncil/aicouncil/internal/security/rbac"
	storage "github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
)

type listEnvelope struct {
	Data  taskListResponse `json:"data"`
	Error *apiError        `json:"error"`
}

func seedListAPITasks(t *testing.T) *API {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "list.db"))
	require.NoError(t, err)
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	workspaces := []storage.WorkspaceRecord{
		{ID: "ws-own", OwnerUserID: "alice"},
		{ID: "ws-shared", OwnerUserID: "bob"},
		{ID: "ws-other", OwnerUserID: "carol"},
	}
	for _, ws := range workspaces {
		require.NoError(t, db.Create(&ws).Error)
	}
	require.NoError(t, db.Create(&storage.WorkspaceShareRecord{WorkspaceID: "ws-shared", UserID: "alice", Access: "viewer", GrantedBy: "bob", CreatedAt: t0, UpdatedAt: t0}).Error)
	tasks := []storage.TaskRecord{
		{ID: "task-own", OwnerUserID: "alice", CreatedBy: "alice", WorkspaceID: "ws-own", Origin: "direct", Title: "Own task", Requirement: "alice requirement", State: "DRAFT", PlanVersion: 1, CreatedAt: t0, UpdatedAt: t0},
		{ID: "task-shared", OwnerUserID: "bob", CreatedBy: "bob", WorkspaceID: "ws-shared", Origin: "direct", Title: "Shared task", Requirement: "bob requirement", State: "AWAITING_APPROVAL", PlanVersion: 1, CreatedAt: t0, UpdatedAt: t0.Add(time.Minute)},
		{ID: "task-other", OwnerUserID: "carol", CreatedBy: "carol", WorkspaceID: "ws-other", Origin: "direct", Title: "Other task", Requirement: "carol requirement", State: "DRAFT", PlanVersion: 1, CreatedAt: t0, UpdatedAt: t0.Add(2 * time.Minute)},
	}
	for _, task := range tasks {
		require.NoError(t, db.Create(&task).Error)
	}
	return NewPersistentAPI(db)
}

func callListTasks(t *testing.T, api *API, query string, identity *rbac.Identity) (int, listEnvelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?"+query, nil)
	if identity != nil {
		req = req.WithContext(context.WithValue(req.Context(), identityContextKey{}, *identity))
	}
	rec := httptest.NewRecorder()
	api.listTasks(rec, req)
	var envelope listEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	return rec.Code, envelope
}

func listIDs(envelope listEnvelope) []string {
	ids := make([]string, 0, len(envelope.Data.Items))
	for _, item := range envelope.Data.Items {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestListTasksRBACMatrix(t *testing.T) {
	api := seedListAPITasks(t)

	// No identity (legacy token / desktop mode): unscoped, matches the
	// long-standing handler semantics for a missing identity.
	code, envelope := callListTasks(t, api, "", nil)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []string{"task-other", "task-shared", "task-own"}, listIDs(envelope))

	// Plain user: own + shared workspaces only.
	alice := rbac.Identity{UserID: "alice", Permissions: []string{"task:read"}}
	code, envelope = callListTasks(t, api, "", &alice)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []string{"task-shared", "task-own"}, listIDs(envelope))

	// task:read:any lifts the scoping.
	aliceAny := rbac.Identity{UserID: "alice", Permissions: []string{"task:read", "task:read:any"}}
	code, envelope = callListTasks(t, api, "", &aliceAny)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, envelope.Data.Items, 3)

	// Another plain user sees only their own workspace's tasks.
	carol := rbac.Identity{UserID: "carol", Permissions: []string{"task:read"}}
	code, envelope = callListTasks(t, api, "", &carol)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []string{"task-other"}, listIDs(envelope))

	// Filtering by an inaccessible workspace is an empty list, not a 403.
	code, envelope = callListTasks(t, api, "workspace_id=ws-other", &alice)
	require.Equal(t, http.StatusOK, code)
	require.Empty(t, envelope.Data.Items)

	// Filters still apply within scope.
	code, envelope = callListTasks(t, api, "state=AWAITING_APPROVAL", &alice)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []string{"task-shared"}, listIDs(envelope))
}

func TestListTasksValidationAndPagination(t *testing.T) {
	api := seedListAPITasks(t)
	for _, query := range []string{"limit=0", "limit=201", "limit=abc", "cursor=garbage", "cursor=aGVsbG8", "updated_after=nope", "updated_before=2026-13-01T00:00:00Z"} {
		code, envelope := callListTasks(t, api, query, nil)
		require.Equal(t, http.StatusBadRequest, code, "query %s", query)
		require.NotNil(t, envelope.Error)
		require.Equal(t, "invalid_task_list", envelope.Error.Code)
	}

	// Walk the keyset pages.
	var seen []string
	cursor := ""
	for i := 0; i < 5; i++ {
		query := "limit=1"
		if cursor != "" {
			query += "&cursor=" + cursor
		}
		code, envelope := callListTasks(t, api, query, nil)
		require.Equal(t, http.StatusOK, code)
		seen = append(seen, listIDs(envelope)...)
		if envelope.Data.NextCursor == "" {
			break
		}
		cursor = envelope.Data.NextCursor
	}
	require.Equal(t, []string{"task-other", "task-shared", "task-own"}, seen)
}

func TestListTasksSummaryShape(t *testing.T) {
	api := seedListAPITasks(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rec := httptest.NewRecorder()
	api.listTasks(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var raw struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	require.Len(t, raw.Data.Items, 3)
	item := raw.Data.Items[0]
	require.Contains(t, item, "requirement_excerpt")
	require.Contains(t, item, "title")
	require.NotContains(t, item, "plan", "summary DTO must not embed the plan")
	require.NotContains(t, item, "requirement", "summary DTO must not embed the full requirement")
}
