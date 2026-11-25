package council

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aicouncil/aicouncil/internal/app/group"
	"github.com/aicouncil/aicouncil/internal/app/wskind"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// taskBridge implements group.TaskBridge over the landed task engine
// (this API) and the extracted cores in routes.go. It is the ONLY code
// that turns council task information into what the group may see:
// state, stage, an adapter-written summary and a failure category —
// never command output, logs, diffs or file contents (W17).
//
// Wiring reality vs the Quick draft (W14/W15/W21): there is no
// task.Service in production; tasks live in API.tasks with TaskRecord
// persistence, approvals live in approveTaskCore (approval.Hash check +
// approval.Signer + ApprovalRepository), and execution in
// executeTaskCore. The bridge calls those cores with a nil access
// check: push-down acts on the group approval's authority (W19), while
// the kind guard, the single-use approval consumption and the Runner
// sandbox all still run inside the cores.
type taskBridge struct {
	api  *API
	poll time.Duration
}

func newTaskBridge(api *API) taskBridge {
	return taskBridge{api: api, poll: 2 * time.Second}
}

func permanent(err error) error { return fmt.Errorf("%w: %v", group.ErrPushdownPermanent, err) }

// isPermanentExecErr reports whether an executeTaskCore failure code is
// a council refusal a retry cannot fix (W24: the landed approval
// package has no ErrConsumed/ErrInvalid sentinels — consumption is
// signalled by the core's "approval_consumed" code, so permanence is
// decided on the failure CODE set).
func isPermanentExecErr(f *execFailure) bool {
	if f == nil {
		return false
	}
	switch f.Code {
	case "workspace_not_executable", "approval_consumed", "approval_required",
		"invalid_state", "not_found", "execution_failed":
		return true
	}
	return false
}

func (b taskBridge) CreatePushdownTask(ctx context.Context, in group.PushdownTaskInput) (string, error) {
	var plan schema.ExecutionPlan
	if err := json.Unmarshal(in.PlanJSON, &plan); err != nil {
		return "", permanent(err)
	}
	// Defense in depth: the target must be executable before a task
	// exists for it (executeTaskCore guards again at execution time).
	if b.api.db != nil {
		if err := wskind.GuardExecution(ctx, sqlite.NewWorkspaceKindStore(b.api.db), in.WorkspaceID); err != nil {
			if errors.Is(err, wskind.ErrNotExecutable) || errors.Is(err, wskind.ErrUnknownKind) {
				return "", permanent(err)
			}
			return "", err
		}
	}
	id, _, err := b.api.createTaskWithPlan(ctx, in.WorkspaceID, in.OwnerID, in.Requirement, in.Acceptance, plan, in.IdempotencyKey, in.GroupRequirementID)
	if err != nil {
		return "", err
	}
	return id, nil
}

// ApproveTask runs the council approval for a push-down task through
// approveTaskCore (single-use record via approval.Signer +
// ApprovalRepository) and returns the approval record ID. The plan-hash
// binding is checked by the caller against TaskStatus.PlanHash before
// this is invoked (pushDown), and approveTaskCore re-checks the task's
// own ApprovalHash.
func (b taskBridge) ApproveTask(ctx context.Context, taskID, approverID string) (string, error) {
	b.api.mu.Lock()
	t := b.api.tasks[taskID]
	if t == nil {
		b.api.mu.Unlock()
		return "", permanent(errors.New("task not found"))
	}
	planVersion, hash := t.PlanVersion, t.ApprovalHash
	b.api.mu.Unlock()
	_, approvalID, fail := b.api.approveTaskCore(ctx, taskID, planVersion, hash, approverID, nil)
	if fail != nil {
		switch fail.Code {
		case "invalid_state", "approval_mismatch", "signing_unavailable":
			return "", permanent(fail)
		}
		return "", fail
	}
	return approvalID, nil
}

func (b taskBridge) ExecuteTask(ctx context.Context, taskID, _ string) error {
	_, fail := b.api.executeTaskCore(ctx, taskID, nil)
	if fail == nil {
		return nil
	}
	if fail.Code == "execution_failed" {
		// The plan ran and failed: permanent, with the runner's error
		// code mapped to a display category (never the raw output).
		return &group.PermanentError{Category: failureCategory("FAILED", fail.Msg), Err: fail}
	}
	if isPermanentExecErr(fail) {
		return permanent(fail)
	}
	return fail
}

func (b taskBridge) CancelTask(ctx context.Context, taskID, _ string) error {
	_, fail := b.api.cancelTaskCore(taskID, nil)
	if fail != nil {
		return fail
	}
	return nil
}

// phaseOf normalizes the landed runstate values; the mapping itself
// lives in group.PhaseOf (single source of truth — the Quick draft's
// PLANNED/APPROVED/COMPLETED names do not exist in runstate).
func phaseOf(state string) string { return group.PhaseOf(state) }

// failureCategory maps a failed task to a display category. The reason
// text itself is NOT forwarded (it can contain command output); only
// keyword classes are read from it.
func failureCategory(state, reason string) string {
	switch {
	case state == "CANCELLED":
		return "cancelled"
	case reason == "":
		return "runner_error"
	case strings.Contains(reason, "verif"):
		return "verification_failed"
	case strings.Contains(reason, "timeout"), strings.Contains(reason, "deadline"):
		return "timeout"
	default:
		return "runner_error"
	}
}

// TaskStatus deliberately exposes only state, stage, a generated
// summary, a failure category and the group plan hash of the task's
// plan (so push-down can prove the task still carries the reviewed
// plan). Landed tasks store no failure-reason text, so a FAILED task
// without verification data reports the generic runner_error category.
func (b taskBridge) TaskStatus(_ context.Context, taskID string) (group.TaskStatus, error) {
	b.api.mu.Lock()
	defer b.api.mu.Unlock()
	t := b.api.tasks[taskID]
	if t == nil {
		return group.TaskStatus{}, errors.New("task not found")
	}
	planJSON, _ := json.Marshal(t.Plan)
	state := t.State
	phase := phaseOf(state)
	st := group.TaskStatus{TaskID: t.ID, State: state, Stage: state, Phase: phase, PlanHash: group.PlanHashOf(planJSON)}
	steps := len(t.Plan.Patches) + len(t.Plan.Commands)
	verifies := len(t.Plan.VerificationCommands)
	switch phase {
	case "pending":
		st.Summary = fmt.Sprintf("已创建，计划包含 %d 个步骤、%d 个验证命令", steps, verifies)
	case "running":
		st.Summary = fmt.Sprintf("正在执行 %d 个步骤", steps)
	case "succeeded":
		st.Summary = fmt.Sprintf("全部 %d 个步骤与 %d 个验证命令已完成", steps, verifies)
	default:
		st.FailureCategory = failureCategory(state, "")
		st.Summary = "未完成：" + st.FailureCategory
	}
	return st, nil
}

// WatchTask polls TaskStatus and emits on every (state, stage) change.
// Polling is used because it needs nothing beyond TaskStatus; a
// subscribe-based version can replace it later without touching group
// code (W18: the landed task engine has no in-process event bus).
func (b taskBridge) WatchTask(ctx context.Context, taskID string) (<-chan group.TaskStatus, error) {
	first, err := b.TaskStatus(ctx, taskID)
	if err != nil {
		return nil, err
	}
	poll := b.poll
	if poll <= 0 {
		poll = 2 * time.Second
	}
	ch := make(chan group.TaskStatus, 8)
	go func() {
		defer close(ch)
		last := ""
		emit := func(st group.TaskStatus) bool {
			key := st.State + "/" + st.Stage
			if key != last {
				last = key
				select {
				case ch <- st:
				case <-ctx.Done():
					return false
				}
			}
			return st.Phase == "pending" || st.Phase == "running"
		}
		if !emit(first) {
			return
		}
		t := time.NewTicker(poll)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				st, err := b.TaskStatus(ctx, taskID)
				if err != nil {
					continue
				}
				if !emit(st) {
					return
				}
			}
		}
	}()
	return ch, nil
}

// modelWSLookup adapts the P4 aimodel service (W12): the workspace ID
// and provisioning status of an AI model's private workspace.
type modelWSLookup struct{ api *API }

func (m modelWSLookup) ModelWorkspace(ctx context.Context, aiModelID string) (string, string, error) {
	v, err := m.api.aiModels.Get(ctx, aiModelID)
	if err != nil {
		return "", "", err
	}
	if v.Workspace == nil {
		return "", "missing", nil
	}
	return v.Workspace.WorkspaceID, v.Workspace.Status, nil
}
