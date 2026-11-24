package council

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// taskSummary is the list DTO: it deliberately excludes the plan body and the
// full requirement text so list pages stay cheap. patch/command counts let
// the UI render an approximate plan scale without hydrating every task.
type taskSummary struct {
	ID                     string    `json:"id"`
	Title                  string    `json:"title"`
	RequirementExcerpt     string    `json:"requirement_excerpt"`
	State                  string    `json:"state"`
	WorkspaceID            string    `json:"workspace_id"`
	CreatedBy              string    `json:"created_by"`
	Origin                 string    `json:"origin"`
	PlanVersion            int       `json:"plan_version"`
	Approved               bool      `json:"approved"`
	AcceptanceCount        int       `json:"acceptance_count"`
	PatchCount             int       `json:"patch_count"`
	CommandCount           int       `json:"command_count"`
	VerificationArtifactID string    `json:"verification_artifact_id,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

type taskListResponse struct {
	Items      []taskSummary `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

func taskTitleOrFallback(title, requirement string) string {
	if strings.TrimSpace(title) != "" {
		return title
	}
	line := strings.TrimSpace(strings.SplitN(requirement, "\n", 2)[0])
	return truncateRunes(line, 120)
}

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}

func summaryFromRecord(record sqlite.TaskRecord) taskSummary {
	var acceptance []string
	_ = json.Unmarshal(record.AcceptanceJSON, &acceptance)
	var plan schema.ExecutionPlan
	_ = json.Unmarshal(record.PlanJSON, &plan)
	verificationArtifactID := ""
	var verification struct {
		RequestId string `json:"request_id"`
	}
	_ = json.Unmarshal(record.VerificationJSON, &verification)
	if verification.RequestId != "" {
		verificationArtifactID = verificationArtifactIDFor(record.ID, record.PlanVersion)
	}
	origin := record.Origin
	if origin == "" {
		origin = "direct"
	}
	createdBy := record.CreatedBy
	if createdBy == "" {
		createdBy = record.OwnerUserID
	}
	return taskSummary{
		ID:                     record.ID,
		Title:                  taskTitleOrFallback(record.Title, record.Requirement),
		RequirementExcerpt:     truncateRunes(strings.TrimSpace(record.Requirement), 200),
		State:                  record.State,
		WorkspaceID:            record.WorkspaceID,
		CreatedBy:              createdBy,
		Origin:                 origin,
		PlanVersion:            record.PlanVersion,
		Approved:               record.ApprovalGranted,
		AcceptanceCount:        len(acceptance),
		PatchCount:             len(plan.Patches),
		CommandCount:           len(plan.Commands) + len(plan.VerificationCommands),
		VerificationArtifactID: verificationArtifactID,
		CreatedAt:              record.CreatedAt,
		UpdatedAt:              record.UpdatedAt,
	}
}

func (t *task) summary() taskSummary {
	origin := t.Origin
	if origin == "" {
		origin = "direct"
	}
	createdBy := t.CreatedBy
	if createdBy == "" {
		createdBy = t.OwnerUserID
	}
	return taskSummary{
		ID:                     t.ID,
		Title:                  taskTitleOrFallback(t.Title, t.Requirement),
		RequirementExcerpt:     truncateRunes(strings.TrimSpace(t.Requirement), 200),
		State:                  t.State,
		WorkspaceID:            t.WorkspaceID,
		CreatedBy:              createdBy,
		Origin:                 origin,
		PlanVersion:            t.PlanVersion,
		Approved:               t.Approved,
		AcceptanceCount:        len(t.Acceptance),
		PatchCount:             len(t.Plan.Patches),
		CommandCount:           len(t.Plan.Commands) + len(t.Plan.VerificationCommands),
		VerificationArtifactID: t.VerificationArtifactID,
		CreatedAt:              t.CreatedAt,
		UpdatedAt:              t.UpdatedAt,
	}
}

type taskListParams struct {
	workspaceIDs []string
	states       []string
	createdBy    string
	origins      []string
	query        string
	after        *time.Time
	before       *time.Time
	limit        int
	cursor       string
}

func multiParam(values []string) []string {
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func parseTaskListParams(r *http.Request) (taskListParams, error) {
	q := r.URL.Query()
	params := taskListParams{
		workspaceIDs: multiParam(q["workspace_id"]),
		states:       multiParam(q["state"]),
		createdBy:    strings.TrimSpace(q.Get("created_by")),
		origins:      multiParam(q["origin"]),
		query:        strings.TrimSpace(q.Get("q")),
		limit:        50,
		cursor:       strings.TrimSpace(q.Get("cursor")),
	}
	if raw := q.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			return params, errors.New("limit must be between 1 and 200")
		}
		params.limit = limit
	}
	for _, item := range []struct {
		name string
		out  **time.Time
	}{
		{"updated_after", &params.after},
		{"updated_before", &params.before},
	} {
		if raw := q.Get(item.name); raw != "" {
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return params, errors.New(item.name + " must be an RFC3339 timestamp")
			}
			*item.out = &parsed
		}
	}
	if params.cursor != "" {
		if _, _, err := sqlite.DecodeTaskCursor(params.cursor); err != nil {
			return params, err
		}
	}
	return params, nil
}

// listScopeUser returns the user the list must be scoped to, or "" when the
// caller may see every task: either the deployment has no RBAC identity
// (legacy token / desktop mode, where handlers already treat the caller as
// unscoped) or the identity holds task:read:any.
func (a *API) listScopeUser(r *http.Request) string {
	identity, ok := IdentityFromRequest(r)
	if !ok {
		return ""
	}
	if identityHasPermission(identity, "task:read:any") {
		return ""
	}
	return identity.UserID
}

func (a *API) listTasks(w http.ResponseWriter, r *http.Request) {
	params, err := parseTaskListParams(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_task_list", err.Error())
		return
	}
	scopeUser := a.listScopeUser(r)
	if a.db != nil {
		page, err := sqlite.NewTaskRepository(a.db).ListTasks(r.Context(), sqlite.TaskListQuery{
			WorkspaceIDs:  params.workspaceIDs,
			States:        params.states,
			CreatedBy:     params.createdBy,
			Origins:       params.origins,
			Query:         params.query,
			UpdatedAfter:  params.after,
			UpdatedBefore: params.before,
			ScopeUserID:   scopeUser,
			Cursor:        params.cursor,
			Limit:         params.limit,
		})
		if err != nil {
			if errors.Is(err, sqlite.ErrInvalidTaskCursor) {
				writeErr(w, http.StatusBadRequest, "invalid_task_list", "cursor is invalid")
				return
			}
			writeErr(w, http.StatusInternalServerError, "task_list_failed", err.Error())
			return
		}
		items := make([]taskSummary, 0, len(page.Tasks))
		for _, record := range page.Tasks {
			items = append(items, summaryFromRecord(record))
		}
		writeData(w, http.StatusOK, taskListResponse{Items: items, NextCursor: page.NextCursor})
		return
	}
	writeData(w, http.StatusOK, a.listTasksFromMemory(r, params, scopeUser))
}

// listTasksFromMemory serves the list from the in-memory map for the
// non-persistent API (desktop/tests). The same filters, ordering and cursor
// semantics apply; the DB remains the source of truth whenever it exists.
func (a *API) listTasksFromMemory(r *http.Request, params taskListParams, scopeUser string) taskListResponse {
	a.mu.Lock()
	all := make([]*task, 0, len(a.tasks))
	for _, t := range a.tasks {
		all = append(all, t)
	}
	a.mu.Unlock()
	workspaceSet := stringSet(params.workspaceIDs)
	stateSet := stringSet(params.states)
	originSet := stringSet(params.origins)
	needle := strings.ToLower(params.query)
	filtered := make([]taskSummary, 0, len(all))
	for _, t := range all {
		summary := t.summary()
		if scopeUser != "" && !a.canReadWorkspace(r.Context(), t.WorkspaceID, scopeUser) {
			continue
		}
		if len(workspaceSet) > 0 && !workspaceSet[summary.WorkspaceID] {
			continue
		}
		if len(stateSet) > 0 && !stateSet[summary.State] {
			continue
		}
		if params.createdBy != "" && summary.CreatedBy != params.createdBy {
			continue
		}
		if len(originSet) > 0 && !originSet[summary.Origin] {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(summary.Title+" "+t.Requirement+" "+summary.ID), needle) {
			continue
		}
		if params.after != nil && !summary.UpdatedAt.After(*params.after) {
			continue
		}
		if params.before != nil && !summary.UpdatedAt.Before(*params.before) {
			continue
		}
		filtered = append(filtered, summary)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if !filtered[i].UpdatedAt.Equal(filtered[j].UpdatedAt) {
			return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
		}
		return filtered[i].ID > filtered[j].ID
	})
	if params.cursor != "" {
		cursorUpdated, cursorID, _ := sqlite.DecodeTaskCursor(params.cursor)
		start := 0
		for start < len(filtered) {
			item := filtered[start]
			if item.UpdatedAt.Before(cursorUpdated) || (item.UpdatedAt.Equal(cursorUpdated) && item.ID < cursorID) {
				break
			}
			start++
		}
		filtered = filtered[start:]
	}
	response := taskListResponse{Items: filtered}
	if len(filtered) > params.limit {
		last := filtered[params.limit-1]
		response.NextCursor = sqlite.EncodeTaskCursor(last.UpdatedAt, last.ID)
		response.Items = filtered[:params.limit]
	}
	if response.Items == nil {
		response.Items = []taskSummary{}
	}
	return response
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}
