package council

import (
	"bytes"
	"context"
	"encoding/json"
	storage "github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/rest/pathvar"
)

func TestPersistentAPIRehydratesTasksAndEvents(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	a := NewPersistentAPI(db)
	routes := a.Routes()
	var create func(http.ResponseWriter, *http.Request)
	for _, r := range routes {
		if r.Method == http.MethodPost && r.Path == "/api/v1/tasks" {
			create = r.Handler
		}
	}
	rec := httptest.NewRecorder()
	create(rec, httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewBufferString(`{"workspace_id":"ws","requirement":"persist","acceptance":["ok"]}`)))
	require.Equal(t, 201, rec.Code)
	var body responseEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	m := body.Data.(map[string]any)
	id := m["id"].(string)
	b := NewPersistentAPI(db)
	require.Contains(t, b.tasks, id)
	ev, err := b.eventRepo.After(context.Background(), id, 0, 10)
	require.NoError(t, err)
	require.NotEmpty(t, ev)
}

func TestTaskLifecycleRequiresApproval(t *testing.T) {
	a := NewAPI()
	routes := a.Routes()
	find := func(method, path string) func(http.ResponseWriter, *http.Request) {
		for _, x := range routes {
			if x.Method == method && x.Path == path {
				return x.Handler
			}
		}
		t.Fatalf("missing route %s %s", method, path)
		return nil
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewBufferString(`{"workspace_id":"ws-1","requirement":"add ready","acceptance":["tests pass"]}`))
	find(http.MethodPost, "/api/v1/tasks")(rec, req)
	require.Equal(t, 201, rec.Code)
	var created responseEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	taskMap := created.Data.(map[string]any)
	id := taskMap["id"].(string)
	withID := func(h func(http.ResponseWriter, *http.Request)) (int, string) {
		r := httptest.NewRecorder()
		q := pathvar.WithVars(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+id, nil), map[string]string{"id": id})
		h(r, q)
		return r.Code, r.Body.String()
	}
	code, _ := withID(find(http.MethodPost, "/api/v1/tasks/:id/execute"))
	require.Equal(t, 403, code)
	code, _ = withID(find(http.MethodPost, "/api/v1/tasks/:id/start"))
	require.Equal(t, 200, code)
	code, _ = withID(find(http.MethodPost, "/api/v1/tasks/:id/approve"))
	require.Equal(t, 400, code)
	r := httptest.NewRecorder()
	q := pathvar.WithVars(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+id, nil), map[string]string{"id": id})
	q.Body = http.NoBody // approval body is supplied below
	q = pathvar.WithVars(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+id, bytes.NewBufferString(`{"plan_version":1,"approval_hash":"hash"}`)), map[string]string{"id": id})
	find(http.MethodPost, "/api/v1/tasks/:id/approve")(r, q)
	require.Equal(t, 200, r.Code)
	code, _ = withID(find(http.MethodPost, "/api/v1/tasks/:id/execute"))
	require.Equal(t, 200, code)
}
