package council

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/rest/pathvar"
)

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
