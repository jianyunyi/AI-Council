package council

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storage "github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/rest/pathvar"
)

func seedRejectableTask(t *testing.T) (*API, string) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "reject.db"))
	require.NoError(t, err)
	pool, _ := db.DB()
	t.Cleanup(func() { _ = pool.Close() })
	a := NewPersistentAPI(db)
	registerWorkspace(a, "ws")
	a.tasks["task-1"] = &task{ID: "task-1", State: "AWAITING_APPROVAL", WorkspaceID: "ws", Requirement: "do the thing", PlanVersion: 2, ApprovalHash: "hash-v2", OwnerUserID: ""}
	require.NoError(t, db.Create(&storage.TaskRecord{ID: "task-1", WorkspaceID: "ws", Requirement: "do the thing", State: "AWAITING_APPROVAL", PlanVersion: 2, ApprovalHash: "hash-v2"}).Error)
	return a, "task-1"
}

func callReject(t *testing.T, a *API, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	reject := routeHandler(t, a.Routes(), http.MethodPost, "/api/v1/tasks/:id/reject")
	rec := httptest.NewRecorder()
	reject(rec, pathvar.WithVars(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+id+"/reject", strings.NewReader(body)), map[string]string{"id": id}))
	return rec
}

func decodeTaskBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct {
		Data  map[string]any `json:"data"`
		Error *apiError      `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	return envelope.Data
}

func TestRejectPersistsReasonAndCategory(t *testing.T) {
	a, id := seedRejectableTask(t)
	rec := callReject(t, a, id, `{"reason":"plan writes outside the workspace","category":"unsafe","plan_version":2}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data := decodeTaskBody(t, rec)
	require.Equal(t, "CANCELLED", data["state"])
	require.Equal(t, float64(1), data["rejection_count"])
	rejections := data["rejections"].([]any)
	require.Len(t, rejections, 1)
	first := rejections[0].(map[string]any)
	require.Equal(t, "unsafe", first["category"])
	require.Equal(t, "plan writes outside the workspace", first["reason"])
	require.Equal(t, float64(2), first["plan_version"])
	require.Equal(t, "hash-v2", first["plan_hash"])
	require.Equal(t, "task", first["source"])

	// GET /tasks/:id/rejections serves the same record.
	list := routeHandler(t, a.Routes(), http.MethodGet, "/api/v1/tasks/:id/rejections")
	rec = httptest.NewRecorder()
	list(rec, pathvar.WithVars(httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+id+"/rejections", nil), map[string]string{"id": id}))
	require.Equal(t, http.StatusOK, rec.Code)
	var listEnvelope struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listEnvelope))
	require.Len(t, listEnvelope.Data, 1)
	require.Equal(t, "unsafe", listEnvelope.Data[0]["category"])

	// The event landed with the design payload.
	events, err := a.eventRepo.After(context.Background(), id, 0, 10)
	require.NoError(t, err)
	var found bool
	for _, event := range events {
		if event.Type == "approval.rejected" {
			found = true
			var payload map[string]any
			require.NoError(t, json.Unmarshal(event.Data, &payload))
			require.Equal(t, "unsafe", payload["category"])
			require.Equal(t, "plan writes outside the workspace", payload["reason_excerpt"])
			require.Equal(t, float64(2), payload["plan_version"])
			require.NotEmpty(t, payload["rejection_id"])
		}
	}
	require.True(t, found, "approval.rejected event must be appended")

	// The list summary carries the last_rejection badge.
	code, envelope := callListTasks(t, a, "", nil)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, envelope.Data.Items, 1)
	require.NotNil(t, envelope.Data.Items[0].LastRejection)
	require.Equal(t, "unsafe", envelope.Data.Items[0].LastRejection.Category)
}

func TestRejectDeprecationAndValidation(t *testing.T) {
	a, id := seedRejectableTask(t)

	// Missing reason: one-release deprecation path stores the placeholder.
	rec := callReject(t, a, id, `{}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data := decodeTaskBody(t, rec)
	rejections := data["rejections"].([]any)
	require.Len(t, rejections, 1)
	require.Equal(t, "(no reason provided)", rejections[0].(map[string]any)["reason"])
	require.Equal(t, "other", rejections[0].(map[string]any)["category"])

	// Stale plan_version conflicts.
	rec = callReject(t, a, id, `{"reason":"again","category":"quality","plan_version":1}`)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

	// Reason length limit and category enum are enforced.
	rec = callReject(t, a, id, `{"reason":"`+strings.Repeat("x", 4001)+`","category":"quality"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	rec = callReject(t, a, id, `{"reason":"fine","category":"bogus"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	rec = callReject(t, a, id, `{not json`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRejectInvalidatesPendingApproval(t *testing.T) {
	a, id := seedRejectableTask(t)
	ctx := context.Background()
	require.NoError(t, a.approvalRepo.Save(ctx, storage.ApprovalRecord{ID: "approval-1", RunID: id, PlanVersion: 2, SnapshotHash: "hash-v2", Decision: "approved", Actor: "user", CreatedAt: time.Now().UTC()}))
	_, err := a.approvalRepo.Current(ctx, id, 2)
	require.NoError(t, err, "approval must be active before the rejection")

	rec := callReject(t, a, id, `{"reason":"not safe","category":"unsafe","plan_version":2}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	_, err = a.approvalRepo.Current(ctx, id, 2)
	require.Error(t, err, "approval must be invalidated by the rejection")
	consumed, err := a.approvalRepo.Consume(ctx, id, 2, "hash-v2")
	require.NoError(t, err)
	require.False(t, consumed, "invalidated approval must not be consumable")
}

func TestRejectAtomicityOnStorageFailure(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "atomic.db"))
	require.NoError(t, err)
	pool, _ := db.DB()
	t.Cleanup(func() { _ = pool.Close() })
	ctx := context.Background()
	require.NoError(t, db.Create(&storage.TaskRecord{ID: "task-1", WorkspaceID: "ws", State: "AWAITING_APPROVAL", PlanVersion: 1, ApprovalHash: "h"}).Error)
	require.NoError(t, db.Create(&storage.ApprovalRecord{ID: "approval-1", RunID: "task-1", PlanVersion: 1, SnapshotHash: "h", Decision: "approved", Actor: "user", CreatedAt: time.Now().UTC()}).Error)
	repo := storage.NewRejectionRepository(db)
	record := storage.TaskRejectionRecord{ID: "rej-fixed", TaskID: "task-1", RunID: "task-1", PlanVersion: 1, RejectedBy: "user", Category: "other", Reason: "first", Source: "task", CreatedAt: time.Now().UTC()}
	_, err = repo.RejectAtomically(ctx, record, "CANCELLED", map[string]any{"rejection_id": "rej-fixed"})
	require.NoError(t, err)

	// A second rejection reusing the same ID must fail and roll back: the
	// approval stays invalidated from the first call, but no new event or
	// state change lands from the failed transaction.
	dupe := record
	dupe.Reason = "second"
	_, err = repo.RejectAtomically(ctx, dupe, "CANCELLED", map[string]any{"rejection_id": "rej-fixed"})
	require.Error(t, err)
	rows, err := repo.ListByTask(ctx, "task-1", 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "first", rows[0].Reason)
	events, err := storage.NewEventRepository(db).After(ctx, "task-1", 0, 10)
	require.NoError(t, err)
	rejected := 0
	for _, event := range events {
		if event.Type == "approval.rejected" {
			rejected++
		}
	}
	require.Equal(t, 1, rejected)
}
