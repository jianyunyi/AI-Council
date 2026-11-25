package council

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/aicouncil/aicouncil/internal/app/group"
	"github.com/aicouncil/aicouncil/internal/council/roles"
	artifactcore "github.com/aicouncil/aicouncil/internal/core/artifact"
	storage "github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/rest/pathvar"
)

// TestGroupResultEndToEnd is the P9 §5.4 scenario: the P8 approve ->
// push-down flow, continued. The executed task's verification report
// is copied into the group manifest (a log artifact is excluded by
// kind), the summary call fails (no provider key is configured) so
// the submission lands facts-only, and a human result review over
// HTTP completes the requirement.
func TestGroupResultEndToEnd(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "p9.db"))
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
	InstallGroupResults(api, svc)

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
	bobMember := memberByHandle["bob@example.com"]
	aiMember := memberByHandle["claude-x"]
	require.NotEmpty(t, aliceMember)
	require.NotEmpty(t, bobMember)
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
		Cycle: 1, CycleStartAttempt: 1,
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
	code, body = callAIModel(t, api.approveGroupRequirement, http.MethodPost, approvePath,
		`{"plan_hash":"`+planHash+`","expected_version":1}`, groupAlice, rvars)
	require.Equal(t, http.StatusOK, code, string(body))

	var tasks []storage.TaskRecord
	require.NoError(t, db.Find(&tasks).Error)
	require.Len(t, tasks, 1)
	task := tasks[0]
	require.Equal(t, 1, runner.calls)

	// The mirror finishes the requirement (executed), which queues
	// the result job in the same transaction.
	deadline := time.Now().Add(15 * time.Second)
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

	// Seed a log artifact for the task (the verification report was
	// saved by the execution itself): logs are never copied up.
	logEnv, err := artifactcore.New("log", task.ID+"-run-log", "", json.RawMessage(`{"lines":["secret output"]}`))
	require.NoError(t, err)
	require.NoError(t, api.artifactRepo.SaveOwned(context.Background(), "u_alice", task.ID, logEnv))

	// The summary call cannot succeed in this harness (no provider
	// key is configured), so pre-spend two of the result job's three
	// attempts: the first failure is then final and the submission
	// commits facts-only instead of retrying with backoff.
	require.NoError(t, db.Model(&storage.GroupJobRecord{}).
		Where("requirement_id = ? AND kind = ?", "req_1", "result").
		Update("attempts", 2).Error)

	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	go group.NewProposalWorker(svc, "w-p9-e2e").Run(workerCtx)

	deadline = time.Now().Add(20 * time.Second)
	for {
		v, err := svc.GetRequirement(context.Background(), group.Actor{UserID: "u_alice"}, g.ID, "req_1")
		require.NoError(t, err)
		if v.State == group.ReqResultSubmitted {
			require.NotNil(t, v.Result)
			require.Equal(t, "facts", v.Result.SummarySource)
			require.Equal(t, group.ExecSucceeded, v.Result.ExecutionOutcome)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("requirement state = %s, want result_submitted", v.State)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// GET results: the verification report is copied, the log is
	// excluded by kind.
	code, body = callAIModel(t, api.listRequirementResults, http.MethodGet,
		"/api/v1/groups/"+g.ID+"/requirements/req_1/results", "", groupAlice, rvars)
	require.Equal(t, http.StatusOK, code, string(body))
	var results struct {
		Data struct {
			Items []group.ResultSubmissionView `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &results))
	require.Len(t, results.Data.Items, 1)
	byName := map[string]group.GroupArtifactView{}
	for _, a := range results.Data.Items[0].Artifacts {
		byName[a.Name] = a
	}
	report, ok := byName["verification-report.json"]
	require.True(t, ok, "artifacts = %v", byName)
	require.Equal(t, "copied", report.Status)
	logArt, ok := byName["log.json"]
	require.True(t, ok, "artifacts = %v", byName)
	require.Equal(t, "excluded_kind", logArt.Status)

	// Download the copied report: bytes match the source envelope
	// content, with the hardening headers.
	srcEnv, err := api.artifactRepo.Load(context.Background(), task.ID+"-verification-v1")
	require.NoError(t, err)
	dvars := map[string]string{"id": g.ID, "rid": "req_1", "aid": report.ID}
	rec := httptest.NewRecorder()
	dreq := httptest.NewRequest(http.MethodGet, "/api/v1/groups/"+g.ID+"/requirements/req_1/artifacts/"+report.ID, nil)
	dreq = pathvar.WithVars(dreq, dvars)
	dreq = dreq.WithContext(context.WithValue(dreq.Context(), identityContextKey{}, *groupAlice))
	api.downloadGroupArtifact(rec, dreq)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, string(srcEnv.Content), rec.Body.String())
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	require.Equal(t, srcEnv.ContentHash, rec.Header().Get("X-Content-SHA256"))

	// A non-member gets 404 for the same artifact.
	code, _ = callAIModel(t, api.downloadGroupArtifact, http.MethodGet,
		"/api/v1/groups/"+g.ID+"/requirements/req_1/artifacts/"+report.ID, "", groupEve, dvars)
	require.Equal(t, http.StatusNotFound, code)

	// Result review over HTTP: bob approves -> completed (accepted).
	code, body = callAIModel(t, api.startResultReview, http.MethodPost,
		"/api/v1/groups/"+g.ID+"/requirements/req_1/result-review",
		`{"reviewer_member_ids":["`+bobMember+`"]}`, groupAlice, rvars)
	require.Equal(t, http.StatusOK, code, string(body))
	code, body = callAIModel(t, api.submitGroupReviewDecision, http.MethodPost,
		"/api/v1/groups/"+g.ID+"/requirements/req_1/review/decision",
		`{"verdict":"approve","rationale":"verified"}`, groupBob, rvars)
	require.Equal(t, http.StatusOK, code, string(body))
	v, err := svc.GetRequirement(context.Background(), group.Actor{UserID: "u_alice"}, g.ID, "req_1")
	require.NoError(t, err)
	require.Equal(t, group.ReqCompleted, v.State)
	require.Equal(t, group.CompletionAccepted, v.CompletionOutcome)

	// The approval stays consumed exactly once.
	execute := routeHandler(t, api.Routes(), http.MethodPost, "/api/v1/tasks/:id/execute")
	rec2 := httptest.NewRecorder()
	execReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+task.ID+"/execute", nil)
	execReq = pathvar.WithVars(execReq, map[string]string{"id": task.ID})
	execReq = execReq.WithContext(context.WithValue(execReq.Context(), identityContextKey{}, *groupAlice))
	execute(rec2, execReq)
	require.Equal(t, http.StatusConflict, rec2.Code, rec2.Body.String())
	require.Equal(t, 1, runner.calls)
}
