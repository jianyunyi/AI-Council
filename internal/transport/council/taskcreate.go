package council

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/aicouncil/aicouncil/internal/approval"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// createTaskWithPlan creates a task whose plan is already decided — the
// group push-down entry point (P8). It is the API-layer counterpart of
// the Quick draft's task.Service.CreateWithPlan: that service is never
// constructed in production (the task engine is this API: in-memory
// task map + TaskRecord persistence loaded by NewPersistentAPI), so the
// method lives here, in the landed idiom.
//
// The task starts in AWAITING_APPROVAL with its client-facing
// ApprovalHash computed exactly like startTask computes it
// (approval.Hash over task ID + workspace + plan), so the ordinary
// approveTaskCore -> executeTaskCore path applies unchanged.
//
// Idempotency: a non-empty externalKey returns the existing task
// (created=false). The key index is in-memory, rebuilt from TaskRecord
// rows at startup, and backed by the unique index on
// task_records.external_key, so a crash between task creation and the
// caller recording the task ID can never produce a duplicate task.
func (a *API) createTaskWithPlan(_ context.Context, workspaceID, ownerUserID, requirementText string, acceptance []string, plan schema.ExecutionPlan, externalKey, groupRequirementID string) (string, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if externalKey != "" {
		if id, ok := a.taskExternalKeys[externalKey]; ok {
			if a.tasks[id] != nil {
				return id, false, nil
			}
		}
	}
	if _, ok := a.workspaces[workspaceID]; !ok {
		return "", false, errors.New("workspace must be registered before creating a task")
	}
	planVersion := plan.Version
	if planVersion <= 0 {
		planVersion = 1
		plan.Version = 1
	}
	if len(plan.Acceptance) == 0 {
		plan.Acceptance = append([]string(nil), acceptance...)
	}
	a.nextID++
	id := "task-" + strconv.FormatInt(a.nextID, 10)
	hash, err := approval.Hash(id, workspaceID, plan)
	if err != nil {
		a.nextID--
		return "", false, err
	}
	now := time.Now().UTC()
	t := &task{
		ID: id, State: "AWAITING_APPROVAL", WorkspaceID: workspaceID,
		Title: taskTitleOrFallback("", requirementText), CreatedBy: ownerUserID,
		Origin: "group_pushdown", Requirement: requirementText,
		Acceptance: append([]string(nil), acceptance...),
		PlanVersion: planVersion, ApprovalHash: hash, Plan: plan,
		OwnerUserID: ownerUserID, ExternalKey: externalKey,
		GroupRequirementID: groupRequirementID, CreatedAt: now, UpdatedAt: now,
	}
	a.tasks[id] = t
	if externalKey != "" {
		a.taskExternalKeys[externalKey] = id
	}
	a.metrics.TasksCreated.Add(1)
	if a.db != nil {
		acceptanceJSON, _ := json.Marshal(t.Acceptance)
		planJSON, _ := json.Marshal(plan)
		rec := &sqlite.TaskRecord{
			ID: id, OwnerUserID: ownerUserID, WorkspaceID: workspaceID,
			Title: t.Title, CreatedBy: ownerUserID, Origin: "group_pushdown",
			Requirement: requirementText, AcceptanceJSON: acceptanceJSON,
			State: t.State, PlanVersion: planVersion, ApprovalHash: hash,
			PlanJSON: planJSON, GroupRequirementID: groupRequirementID,
			CreatedAt: now, UpdatedAt: now,
		}
		if externalKey != "" {
			key := externalKey
			rec.ExternalKey = &key
		}
		if err := a.db.Create(rec).Error; err != nil {
			delete(a.tasks, id)
			delete(a.taskExternalKeys, externalKey)
			a.nextID--
			return "", false, err
		}
	}
	if err := a.append(t, "task.created", t); err != nil {
		return "", false, err
	}
	return id, true, nil
}
