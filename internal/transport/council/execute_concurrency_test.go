package council

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	storage "github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/rest/pathvar"
	"google.golang.org/grpc"
)

// slowRunner blocks inside ExecuteApprovedPlan so we can prove other API calls
// are not blocked while an execution is in flight.
type slowRunner struct{ release chan struct{} }

func (s *slowRunner) RegisterWorkspace(context.Context, *runnerv1.RegisterWorkspaceRequest, ...grpc.CallOption) (*runnerv1.RegisterWorkspaceResponse, error) {
	return &runnerv1.RegisterWorkspaceResponse{}, nil
}
func (s *slowRunner) ExecuteApprovedPlan(_ context.Context, req *runnerv1.ExecuteApprovedPlanRequest, _ ...grpc.CallOption) (*runnerv1.ExecuteApprovedPlanResponse, error) {
	<-s.release
	return &runnerv1.ExecuteApprovedPlanResponse{RequestId: req.RequestId, Status: "SUCCEEDED"}, nil
}

func TestExecuteDoesNotBlockReadsDuringRunnerCall(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	pool, _ := db.DB()
	t.Cleanup(func() { _ = pool.Close() })
	runner := &slowRunner{release: make(chan struct{})}
	a := NewPersistentAPI(db).WithRunnerClient(runner)
	registerWorkspace(a, "ws")
	a.tasks["task-1"] = &task{ID: "task-1", State: "AWAITING_APPROVAL", WorkspaceID: "ws", PlanVersion: 1, ApprovalHash: "hash", Approved: true}
	require.NoError(t, db.Create(&storage.TaskRecord{ID: "task-1", WorkspaceID: "ws", State: "AWAITING_APPROVAL", PlanVersion: 1, ApprovalHash: "hash", ApprovalGranted: true}).Error)
	require.NoError(t, a.approvalRepo.Save(context.Background(), storage.ApprovalRecord{ID: "approval-1", RunID: "task-1", PlanVersion: 1, SnapshotHash: "hash", Decision: "approved", Actor: "user"}))

	execute := routeHandler(t, a.Routes(), http.MethodPost, "/api/v1/tasks/:id/execute")
	getTask := routeHandler(t, a.Routes(), http.MethodGet, "/api/v1/tasks/:id")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		rec := httptest.NewRecorder()
		execute(rec, pathvar.WithVars(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-1/execute", nil), map[string]string{"id": "task-1"}))
	}()

	// While execution is blocked in the Runner, a read must still return promptly.
	done := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		getTask(rec, pathvar.WithVars(httptest.NewRequest(http.MethodGet, "/api/v1/tasks/task-1", nil), map[string]string{"id": "task-1"}))
		done <- rec.Code
	}()
	select {
	case code := <-done:
		require.Equal(t, http.StatusOK, code)
	case <-time.After(2 * time.Second):
		t.Fatal("read blocked while execution held the lock")
	}
	close(runner.release)
	wg.Wait()
}
