package council

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

var (
	errReasonTooLong   = errors.New("reason must be at most 4000 characters")
	errInvalidCategory = errors.New("category must be one of incorrect, unsafe, scope, quality, other")
)

type taskRejectionDTO struct {
	ID              string    `json:"id"`
	TaskID          string    `json:"task_id"`
	RunID           string    `json:"run_id"`
	PlanVersion     int       `json:"plan_version"`
	ApprovalNonceID string    `json:"approval_nonce_id,omitempty"`
	PlanHash        string    `json:"plan_hash,omitempty"`
	RejectedBy      string    `json:"rejected_by"`
	Category        string    `json:"category"`
	Reason          string    `json:"reason"`
	Source          string    `json:"source"`
	CreatedAt       time.Time `json:"created_at"`
}

func rejectionDTO(record sqlite.TaskRejectionRecord) taskRejectionDTO {
	nonce := ""
	if record.ApprovalNonceID != nil {
		nonce = *record.ApprovalNonceID
	}
	return taskRejectionDTO{
		ID: record.ID, TaskID: record.TaskID, RunID: record.RunID,
		PlanVersion: record.PlanVersion, ApprovalNonceID: nonce, PlanHash: record.PlanHash,
		RejectedBy: record.RejectedBy, Category: record.Category, Reason: record.Reason,
		Source: record.Source, CreatedAt: record.CreatedAt,
	}
}

type rejectTaskRequest struct {
	Reason      string `json:"reason"`
	Category    string `json:"category"`
	PlanVersion *int   `json:"plan_version"`
}

// rejectionRecords returns the task's rejections newest first, from SQLite
// when persistent and from the in-memory store otherwise.
func (a *API) rejectionRecords(ctx context.Context, taskID string, limit int) []sqlite.TaskRejectionRecord {
	if a.db != nil {
		rows, err := sqlite.NewRejectionRepository(a.db).ListByTask(ctx, taskID, limit)
		if err != nil {
			return nil
		}
		return rows
	}
	stored := a.rejections[taskID]
	out := make([]sqlite.TaskRejectionRecord, 0, len(stored))
	for i := len(stored) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, stored[i])
	}
	return out
}

func (a *API) rejectionCount(ctx context.Context, taskID string) int {
	if a.db != nil {
		count, err := sqlite.NewRejectionRepository(a.db).CountByTask(ctx, taskID)
		if err != nil {
			return 0
		}
		return int(count)
	}
	return len(a.rejections[taskID])
}

// attachRejections populates the detail fields served by GET /tasks/:id.
func (a *API) attachRejections(ctx context.Context, t *task) {
	records := a.rejectionRecords(ctx, t.ID, 20)
	dtos := make([]taskRejectionDTO, 0, len(records))
	for _, record := range records {
		dtos = append(dtos, rejectionDTO(record))
	}
	t.Rejections = dtos
	t.RejectionCount = a.rejectionCount(ctx, t.ID)
}

// attachLastRejections fills the summary badge data for one list page.
func (a *API) attachLastRejections(ctx context.Context, items []taskSummary) {
	if len(items) == 0 {
		return
	}
	if a.db != nil {
		ids := make([]string, 0, len(items))
		for _, item := range items {
			ids = append(ids, item.ID)
		}
		latest, err := sqlite.NewRejectionRepository(a.db).LatestByTaskIDs(ctx, ids)
		if err != nil {
			return
		}
		for i := range items {
			if record, ok := latest[items[i].ID]; ok {
				items[i].LastRejection = &lastRejectionRef{Category: record.Category, CreatedAt: record.CreatedAt}
			}
		}
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range items {
		if stored := a.rejections[items[i].ID]; len(stored) > 0 {
			last := stored[len(stored)-1]
			items[i].LastRejection = &lastRejectionRef{Category: last.Category, CreatedAt: last.CreatedAt}
		}
	}
}

func (a *API) listTaskRejections(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.tasks[taskID(r)]
	if t == nil || !a.canReadWorkspace(r.Context(), t.WorkspaceID, requestOwnerID(r)) {
		writeErr(w, http.StatusNotFound, "not_found", "task not found")
		return
	}
	records := a.rejectionRecords(r.Context(), t.ID, 200)
	dtos := make([]taskRejectionDTO, 0, len(records))
	for _, record := range records {
		dtos = append(dtos, rejectionDTO(record))
	}
	writeData(w, http.StatusOK, dtos)
}

// parseRejectRequest tolerates an empty body (older clients posted nothing);
// a present-but-malformed body is a 400.
func parseRejectRequest(r *http.Request) (rejectTaskRequest, error) {
	var in rejectTaskRequest
	if r.Body == nil {
		return in, nil
	}
	err := json.NewDecoder(r.Body).Decode(&in)
	if err == io.EOF {
		return in, nil
	}
	return in, err
}

var validRejectionCategory = sqlite.RejectionCategories

func normalizeRejection(in rejectTaskRequest) (reason, category string, err error) {
	reason = strings.TrimSpace(in.Reason)
	if reason == "" {
		// One-release deprecation path: clients that predate mandatory
		// reasons still reject successfully, recorded as an empty reason.
		return "(no reason provided)", "other", nil
	}
	if len([]rune(reason)) > 4000 {
		return "", "", errReasonTooLong
	}
	category = strings.TrimSpace(in.Category)
	if category == "" {
		category = "other"
	}
	if !validRejectionCategory[category] {
		return "", "", errInvalidCategory
	}
	return reason, category, nil
}
