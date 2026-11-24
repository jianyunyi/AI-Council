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

// End-to-end handler test for Part B P7: seed a proposed requirement
// directly in the store (the proposal worker is not started in handler
// tests), then drive the review endpoints with a human reviewer.
func TestGroupReviewEndpointsEndToEnd(t *testing.T) {
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

	now := time.Now().UTC()
	require.NoError(t, db.Create(&storage.GroupRequirementRecord{
		ID: "req_1", GroupID: g.ID, AnchorMessageID: "msg_anchor",
		RequestedByUserID: "u_alice", RequestedByMemberID: aliceMember,
		AssigneeMemberID: aiMember, AssigneeAIModelID: "aim_1", AssigneeHandle: "claude-x",
		Title: "plan the migration", BodyMD: "@claude-x plan the migration",
		State: group.ReqProposed, CurrentProposalID: "prop_1", Version: 1,
		CreatedAt: now, UpdatedAt: now, StateChangedAt: now, ProposedAt: &now,
	}).Error)
	require.NoError(t, db.Create(&storage.RequirementProposalRecord{
		ID: "prop_1", RequirementID: "req_1", GroupID: g.ID, Attempt: 1,
		PlanJSON:          []byte(`{"version":1,"commands":[{"executable":"echo","args":["hi"],"timeout_seconds":60,"purpose":"demo"}]}`),
		Summary:           "1 command(s)",
		CreatedByMemberID: aiMember, CreatedAt: now,
	}).Error)

	rvars := map[string]string{"id": g.ID, "rid": "req_1"}
	reviewPath := "/api/v1/groups/" + g.ID + "/requirements/req_1/review"

	// Non-member: 404.
	code, _ = callAIModel(t, api.getGroupReview, http.MethodGet, reviewPath, "", groupEve, rvars)
	require.Equal(t, http.StatusNotFound, code)

	// Open a round with bob as the (human) reviewer.
	code, body = callAIModel(t, api.startGroupReview, http.MethodPost, reviewPath,
		`{"reviewer_member_ids":["`+bobMember+`"]}`, groupAlice, rvars)
	require.Equal(t, http.StatusOK, code, string(body))
	var st struct {
		Data group.ReviewStateView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &st))
	require.Equal(t, group.ReqInReview, st.Data.State)
	require.Len(t, st.Data.Rounds, 1)
	require.Equal(t, group.RoundOpen, st.Data.Rounds[0].Status)

	// A second round while one is open: the requirement has already
	// left "proposed", so this is a state conflict.
	code, body = callAIModel(t, api.startGroupReview, http.MethodPost, reviewPath,
		`{"reviewer_member_ids":["`+bobMember+`"]}`, groupAlice, rvars)
	require.Equal(t, http.StatusConflict, code, string(body))
	require.Contains(t, string(body), "requirement_state_conflict")

	// Bob's view: he can decide, and no verdict is visible yet.
	code, body = callAIModel(t, api.getGroupReview, http.MethodGet, reviewPath, "", groupBob, rvars)
	require.Equal(t, http.StatusOK, code, string(body))
	require.NoError(t, json.Unmarshal(body, &st))
	require.True(t, st.Data.CanDecide)
	require.NotContains(t, string(body), `"verdict"`)

	// Alice is not a reviewer: 403 not_a_reviewer.
	code, body = callAIModel(t, api.submitGroupReviewDecision, http.MethodPost, reviewPath+"/decision",
		`{"verdict":"approve"}`, groupAlice, rvars)
	require.Equal(t, http.StatusForbidden, code, string(body))
	require.Contains(t, string(body), "not_a_reviewer")

	// Bob approves: unanimous over one reviewer -> review_passed, and
	// the verdict is now revealed.
	code, body = callAIModel(t, api.submitGroupReviewDecision, http.MethodPost, reviewPath+"/decision",
		`{"verdict":"approve","rationale":"ok"}`, groupBob, rvars)
	require.Equal(t, http.StatusOK, code, string(body))
	require.NoError(t, json.Unmarshal(body, &st))
	require.Equal(t, group.ReqReviewPassed, st.Data.State)
	require.Equal(t, group.OutcomePassed, st.Data.Rounds[0].Outcome)
	require.Equal(t, group.VerdictApprove, st.Data.Rounds[0].Decisions[0].Verdict)

	// Force-close with no open round: 409 no_open_review.
	code, body = callAIModel(t, api.forceCloseGroupReview, http.MethodPost, reviewPath+"/force-close", "", groupAlice, rvars)
	require.Equal(t, http.StatusConflict, code, string(body))
	require.Contains(t, string(body), "no_open_review")

	// Revise from review_passed: state conflict (only changes_requested).
	code, body = callAIModel(t, api.reviseGroupRequirement, http.MethodPost,
		"/api/v1/groups/"+g.ID+"/requirements/req_1/revise", `{"note":"x"}`, groupAlice, rvars)
	require.Equal(t, http.StatusConflict, code, string(body))
	require.Contains(t, string(body), "requirement_state_conflict")
}
