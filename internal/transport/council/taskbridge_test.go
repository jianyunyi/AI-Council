package council

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/aicouncil/aicouncil/internal/app/group"
	"github.com/aicouncil/aicouncil/internal/app/wskind"
	"github.com/aicouncil/aicouncil/internal/council/roles"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	storage "github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/rest/pathvar"
)

// Part B P8 transport tests: the phase mapping over the LANDED
// runstate names, the kind-guard permanence, CreateWithPlan
// idempotency, and the full HTTP approve -> push-down flow (§5.3).

func TestPhaseOfMapsLandedRunstates(t *testing.T) {
	// The Quick draft guessed PLANNED/APPROVED/COMPLETED; the landed
	// runstate names are these. Unknown states must map to running so
	// they can never finish a requirement by mistake.
	require.Equal(t, "running", phaseOf(""))
	require.Equal(t, "pending", phaseOf("AWAITING_APPROVAL"))
	require.Equal(t, "pending", phaseOf("DRAFT"))
	require.Equal(t, "running", phaseOf("EXECUTING"))
	require.Equal(t, "running", phaseOf("VERIFYING"))
	require.Equal(t, "running", phaseOf("SOMETHING_NEW"))
	require.Equal(t, "succeeded", phaseOf("SUCCEEDED"))
	require.Equal(t, "failed", phaseOf("FAILED"))
	require.Equal(t, "cancelled", phaseOf("CANCELLED"))
}

func TestKindGuardRefusalIsPermanent(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	pool, _ := db.DB()
	t.Cleanup(func() { _ = pool.Close() })
	require.NoError(t, db.Create(&storage.WorkspaceRecord{
		ID: "ws-shared", CanonicalRoot: "/workspace", Kind: string(wskind.GroupShared),
	}).Error)
	a := NewPersistentAPI(db)
	bridge := newTaskBridge(a)
	_, err = bridge.CreatePushdownTask(context.Background(), group.PushdownTaskInput{
		IdempotencyKey: "pd:req_x:1", WorkspaceID: "ws-shared", OwnerID: "u_alice",
		Requirement: "x", PlanJSON: []byte(`{"version":1}`), GroupRequirementID: "req_x",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, group.ErrPushdownPermanent),
		"a group_shared target must be a permanent refusal, got: %v", err)

	// Permanence of execute-core failures is decided on the failure
	// CODE (the landed approval package has no ErrConsumed sentinel).
	require.True(t, isPermanentExecErr(&execFailure{Code: "approval_consumed"}))
	require.True(t, isPermanentExecErr(&execFailure{Code: "workspace_not_executable"}))
	require.False(t, isPermanentExecErr(&execFailure{Code: "runner_unavailable"}))
	require.False(t, isPermanentExecErr(nil))
}

func TestCreateTaskWithPlanIdempotent(t *testing.T) {
	a := NewAPI()
	registerWorkspace(a, "ws-1")
	plan := schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "echo", Args: []string{"hi"}}}}
	id1, created1, err := a.createTaskWithPlan(context.Background(), "ws-1", "u_alice", "do it", nil, plan, "pd:req_1:1", "req_1")
	require.NoError(t, err)
	require.True(t, created1)
	id2, created2, err := a.createTaskWithPlan(context.Background(), "ws-1", "u_alice", "do it", nil, plan, "pd:req_1:1", "req_1")
	require.NoError(t, err)
	require.False(t, created2)
	require.Equal(t, id1, id2)
}

// TestGroupApprovePushdownEndToEnd is the §5.3 scenario: a requirement
// in awaiting_approval (passing round + clear red-team check seeded
// directly, as in the P7 handler tests) is approved over HTTP; the
// task must land in the assignee's MODEL workspace, the group_shared
// workspace must never be executed, and the approval must be consumed
// exactly once.
func TestGroupApprovePushdownEndToEnd(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "p8.db"))
	require.NoError(t, err)
	pool, _ := db.DB()
	t.Cleanup(func() { _ = pool.Close() })
	for _, u := range []storage.UserRecord{
		{ID: "u_alice", Subject: "alice@example.com"},
		{ID: "u_bob", Subject: "bob@example.com"},
	} {
		require.NoError(t, db.Create(&u).Error)
	}
	require.NoError(t, db.Create(&storage.WorkspaceRecord{
		ID: "workspace-aim-1", OwnerUserID: "u_alice", CanonicalRoot: "/workspace",
		RunnerPath: "models/claude-x", Kind: "model_private",
	}).Error)
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

	runner := &routeRunner{}
	api := NewPersistentAPI(db).WithRunnerClient(runner).WithApprovalSigner(testApprovalSigner(t))
	require.NotNil(t, InstallAIModels(api))
	svc := InstallGroups(api)
	require.NotNil(t, svc)
	resolver := roles.NewResolver(storage.NewCouncilRolesRepository(db),
		func(context.Context, string) ([]roles.ProfileInfo, error) {
			return []roles.ProfileInfo{{ID: "prof_1", Provider: "anthropic", Model: "claude-x-1", HasKey: true}}, nil
		})
	InstallGroupPushdown(api, svc, resolver)

	code, body := callAIModel(t, api.createGroup, http.MethodPost, "/api/v1/groups",
		`{"name":"Team","members":[{"member_type":"user","user_id":"u_bob","group_role":"operator"},{"member_type":"ai","ai_model_id":"aim_1"}]}`, groupAlice, nil)
	require.Equal(t, http.StatusCreated, code, string(body))
	var created struct {
		Data group.GroupDetail `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &created))
	g := created.Data
	memberByHandle := map[string]string{}
	for _, m := range g.Members {
		memberByHandle[m.Handle] = m.ID
	}
	aliceMember := memberByHandle["alice@example.com"]
	aiMember := memberByHandle["claude-x"]
	require.NotEmpty(t, aliceMember)
	require.NotEmpty(t, aiMember)

	planJSON := []byte(`{"version":1,"commands":[{"executable":"echo","args":["hi"],"timeout_seconds":60,"purpose":"demo"}]}`)
	planHash := group.PlanHashOf(planJSON)
	now := time.Now().UTC()
	require.NoError(t, db.Create(&storage.GroupRequirementRecord{
		ID: "req_1", GroupID: g.ID, AnchorMessageID: "msg_anchor",
		RequestedByUserID: "u_alice", RequestedByMemberID: aliceMember,
		AssigneeMemberID: aiMember, AssigneeAIModelID: "aim_1", AssigneeHandle: "claude-x",
		Title: "plan the migration", BodyMD: "@claude-x plan the migration",
		State: group.ReqAwaitingApproval, CurrentProposalID: "prop_1", Version: 1,
		CreatedAt: now, UpdatedAt: now, StateChangedAt: now, ProposedAt: &now,
	}).Error)
	require.NoError(t, db.Create(&storage.RequirementProposalRecord{
		ID: "prop_1", RequirementID: "req_1", GroupID: g.ID, Attempt: 1,
		PlanJSON: planJSON, Summary: "1 command(s)", CreatedByMemberID: aiMember, CreatedAt: now,
	}).Error)
	require.NoError(t, db.Create(&storage.ReviewRoundRecord{
		ID: "round_1", GroupID: g.ID, RequirementID: "req_1", ProposalID: "prop_1",
		ProposalRound: 1, PlanHash: planHash, Status: "closed", Outcome: "passed",
		PolicyRule: "unanimous", StartedByUserID: "u_alice", StartedByMember: aliceMember,
		CreatedAt: now, ClosedAt: &now, ClosedBy: "u_bob",
	}).Error)
	require.NoError(t, db.Create(&storage.RedTeamCheckRecord{
		ID: "rt_1", GroupID: g.ID, RequirementID: "req_1", ProposalID: "prop_1",
		ProposalAttempt: 1, ReviewRoundID: "round_1", PlanHash: planHash,
		Status: "clear", Independence: "full", CreatedAt: now, FinishedAt: &now,
	}).Error)

	rvars := map[string]string{"id": g.ID, "rid": "req_1"}
	approvePath := "/api/v1/groups/" + g.ID + "/requirements/req_1/approve"

	// A viewer-less non-member cannot approve: 404.
	code, _ = callAIModel(t, api.approveGroupRequirement, http.MethodPost, approvePath,
		`{"plan_hash":"`+planHash+`"}`, groupEve, rvars)
	require.Equal(t, http.StatusNotFound, code)

	// Alice (owner) approves: push-down runs in the same request.
	code, body = callAIModel(t, api.approveGroupRequirement, http.MethodPost, approvePath,
		`{"plan_hash":"`+planHash+`","expected_version":1}`, groupAlice, rvars)
	require.Equal(t, http.StatusOK, code, string(body))
	var view struct {
		Data group.RequirementView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &view))
	require.Contains(t, []string{group.ReqExecuting, group.ReqExecuted}, view.Data.State)

	// Exactly one task, in the MODEL workspace, keyed for idempotency.
	var tasks []storage.TaskRecord
	require.NoError(t, db.Find(&tasks).Error)
	require.Len(t, tasks, 1)
	task := tasks[0]
	require.Equal(t, "workspace-aim-1", task.WorkspaceID)
	require.NotNil(t, task.ExternalKey)
	require.Equal(t, "pd:req_1:1", *task.ExternalKey)
	require.Equal(t, "group_pushdown", task.Origin)
	require.Equal(t, "req_1", task.GroupRequirementID)
	require.Equal(t, 1, runner.calls, "the runner must run exactly once")

	// The mirror finishes the requirement.
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, err := svc.GetRequirement(context.Background(), group.Actor{UserID: "u_alice"}, g.ID, "req_1")
		require.NoError(t, err)
		if v.State == group.ReqExecuted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("requirement state = %s, want executed", v.State)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// A second execute against the consumed approval/task is refused
	// and the runner does not run again. (With the task SUCCEEDED the
	// landed core answers 409 invalid_state; the approval_consumed code
	// arises in the AWAITING_APPROVAL + already-consumed window and is
	// covered by execute_concurrency_test.go. Either way: one run.)
	execute := routeHandler(t, api.Routes(), http.MethodPost, "/api/v1/tasks/:id/execute")
	rec := httptest.NewRecorder()
	execReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+task.ID+"/execute", nil)
	execReq = pathvar.WithVars(execReq, map[string]string{"id": task.ID})
	execReq = execReq.WithContext(context.WithValue(execReq.Context(), identityContextKey{}, *groupAlice))
	execute(rec, execReq)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Equal(t, 1, runner.calls)
}
