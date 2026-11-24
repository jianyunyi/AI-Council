package council

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aicouncil/aicouncil/internal/app/group"
	"github.com/aicouncil/aicouncil/internal/security/rbac"
	storage "github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/rest/pathvar"
	"gorm.io/gorm"
)

// End-to-end handler test for Part B P5 in RBAC mode: real persistent API,
// real group service and adapters, identities injected the same way the
// auth middleware injects them.
func seedGroupsAPI(t *testing.T) (*API, *gorm.DB) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "groups.db"))
	require.NoError(t, err)
	for _, u := range []storage.UserRecord{
		{ID: "u_alice", Subject: "alice@example.com"},
		{ID: "u_bob", Subject: "bob@example.com"},
	} {
		require.NoError(t, db.Create(&u).Error)
	}
	api := NewPersistentAPI(db)
	require.NotNil(t, InstallAIModels(api))
	require.NotNil(t, InstallGroups(api))
	return api, db
}

var (
	groupAlice = &rbac.Identity{UserID: "u_alice", Permissions: []string{"group:create"}}
	groupBob   = &rbac.Identity{UserID: "u_bob", Permissions: []string{}}
	groupEve   = &rbac.Identity{UserID: "u_eve", Permissions: []string{}}
)

func TestGroupEndpointsEndToEnd(t *testing.T) {
	api, db := seedGroupsAPI(t)

	// Create: alice becomes owner; a group_shared workspace is persisted.
	code, body := callAIModel(t, api.createGroup, http.MethodPost, "/api/v1/groups",
		`{"name":"Team","members":[{"member_type":"user","user_id":"u_bob","group_role":"operator"}]}`, groupAlice, nil)
	require.Equal(t, http.StatusCreated, code, string(body))
	var created struct {
		Data group.GroupDetail `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &created))
	g := created.Data
	require.Equal(t, group.RoleOwner, g.MyRole)
	require.Len(t, g.Members, 2)
	require.NotEmpty(t, g.SharedWorkspaceID)
	var ws storage.WorkspaceRecord
	require.NoError(t, db.First(&ws, "id = ?", g.SharedWorkspaceID).Error)
	require.Equal(t, "group_shared", ws.Kind)

	vars := map[string]string{"id": g.ID}

	// Eve is not a member: 404, never 403.
	code, _ = callAIModel(t, api.getGroup, http.MethodGet, "/api/v1/groups/"+g.ID, "", groupEve, vars)
	require.Equal(t, http.StatusNotFound, code)
	code, _ = callAIModel(t, api.postGroupMessage, http.MethodPost, "/api/v1/groups/"+g.ID+"/messages",
		`{"body_md":"hi","client_msg_id":"e1"}`, groupEve, vars)
	require.Equal(t, http.StatusNotFound, code)

	// Bob posts; an identical retry is a duplicate (200, same message).
	code, body = callAIModel(t, api.postGroupMessage, http.MethodPost, "/api/v1/groups/"+g.ID+"/messages",
		`{"body_md":"hello @alice","client_msg_id":"c1"}`, groupBob, vars)
	require.Equal(t, http.StatusCreated, code, string(body))
	var posted struct {
		Data group.PostMessageResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &posted))
	require.False(t, posted.Data.Duplicate)
	require.Equal(t, "bob@example.com", posted.Data.Message.Author.Handle)
	code, body = callAIModel(t, api.postGroupMessage, http.MethodPost, "/api/v1/groups/"+g.ID+"/messages",
		`{"body_md":"hello @alice","client_msg_id":"c1"}`, groupBob, vars)
	require.Equal(t, http.StatusOK, code)
	require.NoError(t, json.Unmarshal(body, &posted))
	require.True(t, posted.Data.Duplicate)

	// P6 semantics are locked off: dispatch intent and requirement links 422.
	code, body = callAIModel(t, api.postGroupMessage, http.MethodPost, "/api/v1/groups/"+g.ID+"/messages",
		`{"body_md":"do it","client_msg_id":"c2","intent":"dispatch"}`, groupBob, vars)
	require.Equal(t, http.StatusUnprocessableEntity, code, string(body))
	require.Contains(t, string(body), "intent_not_available")
	code, body = callAIModel(t, api.postGroupMessage, http.MethodPost, "/api/v1/groups/"+g.ID+"/messages",
		`{"body_md":"x","client_msg_id":"c3","requirement_id":"req_1"}`, groupBob, vars)
	require.Equal(t, http.StatusUnprocessableEntity, code, string(body))
	require.Contains(t, string(body), "requirement_not_available")
}

func TestGroupEventsStreamReplaysAnd404s(t *testing.T) {
	api, _ := seedGroupsAPI(t)
	code, body := callAIModel(t, api.createGroup, http.MethodPost, "/api/v1/groups", `{"name":"S"}`, groupAlice, nil)
	require.Equal(t, http.StatusCreated, code, string(body))
	var created struct {
		Data group.GroupDetail `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &created))
	gid := created.Data.ID
	vars := map[string]string{"id": gid}

	// Non-member: the stream endpoint answers 404 before any streaming.
	code, _ = callAIModel(t, api.groupEvents, http.MethodGet, "/api/v1/groups/"+gid+"/events", "", groupEve, vars)
	require.Equal(t, http.StatusNotFound, code)

	// Member with a cancelable context: the handler replays group.created
	// (seq 1) and then blocks; cancel ends it.
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups/"+gid+"/events", nil).WithContext(ctx)
	req = req.WithContext(context.WithValue(req.Context(), identityContextKey{}, *groupAlice))
	req = pathvar.WithVars(req, vars)
	rec := &lockedRecorder{header: http.Header{}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		api.groupEvents(rec, req)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(rec.String(), `"seq":1`) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	out := rec.String()
	require.Contains(t, out, "id: 1\n")
	require.Contains(t, out, `"type":"group.created"`)
	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
}

// lockedRecorder is a goroutine-safe http.ResponseWriter + Flusher for the
// streaming handler test.
type lockedRecorder struct {
	mu     sync.Mutex
	header http.Header
	buf    bytes.Buffer
	code   int
}

func (r *lockedRecorder) Header() http.Header { return r.header }
func (r *lockedRecorder) WriteHeader(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.code = code
}
func (r *lockedRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(p)
}
func (r *lockedRecorder) Flush() {}
func (r *lockedRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}
