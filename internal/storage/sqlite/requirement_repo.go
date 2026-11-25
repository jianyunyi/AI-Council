package sqlite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Sentinel errors for the P6 store, following the ErrGroup* convention of
// group_repo.go (the sqlite package has no generic ErrNotFound).
var (
	ErrRequirementNotFound = errors.New("requirement not found")
	ErrProposalDuplicate   = errors.New("proposal already exists for requirement and attempt")
)

func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// RequirementRepo persists P6 requirements, proposals, jobs and the
// invocation ledger. It is constructed over GroupRepo.DB() (possibly a
// transaction handle) so the service can write a requirement + its job +
// its card message + its event in one transaction.
type RequirementRepo struct{ db *gorm.DB }

func NewRequirementRepo(db *gorm.DB) *RequirementRepo { return &RequirementRepo{db: db} }

// ---- requirements ----

func (r *RequirementRepo) CreateRequirement(ctx context.Context, rec *GroupRequirementRecord) error {
	if err := r.db.WithContext(ctx).Create(rec).Error; err != nil {
		return fmt.Errorf("create requirement: %w", err)
	}
	return nil
}

func (r *RequirementRepo) GetRequirement(ctx context.Context, id string) (*GroupRequirementRecord, error) {
	var rec GroupRequirementRecord
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRequirementNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get requirement: %w", err)
	}
	return &rec, nil
}

func (r *RequirementRepo) GetRequirementInGroup(ctx context.Context, groupID, id string) (*GroupRequirementRecord, error) {
	var rec GroupRequirementRecord
	err := r.db.WithContext(ctx).Where("id = ? AND group_id = ?", id, groupID).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRequirementNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get requirement in group: %w", err)
	}
	return &rec, nil
}

// RequirementFilter for ListRequirements. State "" = any; AssigneeMemberID
// "" = any. Keyset cursor over (created_at, id) descending (newest first).
type RequirementFilter struct {
	GroupID          string
	State            string
	AssigneeMemberID string
	CursorCreatedAt  time.Time
	CursorID         string
	Limit            int
}

func (r *RequirementRepo) ListRequirements(ctx context.Context, f RequirementFilter) ([]GroupRequirementRecord, bool, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Model(&GroupRequirementRecord{}).Where("group_id = ?", f.GroupID)
	if f.State != "" {
		q = q.Where("state = ?", f.State)
	}
	if f.AssigneeMemberID != "" {
		q = q.Where("assignee_member_id = ?", f.AssigneeMemberID)
	}
	if f.CursorID != "" {
		q = q.Where("(created_at < ?) OR (created_at = ? AND id < ?)", f.CursorCreatedAt, f.CursorCreatedAt, f.CursorID)
	}
	var out []GroupRequirementRecord
	if err := q.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&out).Error; err != nil {
		return nil, false, fmt.Errorf("list requirements: %w", err)
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// UpdateRequirementState is a compare-and-swap on (id, version): it applies
// the field updates and bumps version only if the row is still at the
// expected version AND in one of fromStates. Returns false when the CAS
// lost (concurrent transition) — callers treat that as "someone else moved
// it" and re-read.
func (r *RequirementRepo) UpdateRequirementState(ctx context.Context, id string, fromStates []string, expectedVersion int, fields map[string]any) (bool, error) {
	updates := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		updates[k] = v
	}
	updates["version"] = expectedVersion + 1
	res := r.db.WithContext(ctx).Model(&GroupRequirementRecord{}).
		Where("id = ? AND version = ? AND state IN ?", id, expectedVersion, fromStates).
		Updates(updates)
	if res.Error != nil {
		return false, fmt.Errorf("update requirement state: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// ---- proposals ----

func (r *RequirementRepo) CreateProposal(ctx context.Context, rec *RequirementProposalRecord) error {
	res := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "requirement_id"}, {Name: "attempt"}},
		DoNothing: true,
	}).Create(rec)
	if res.Error != nil {
		return fmt.Errorf("create proposal: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrProposalDuplicate
	}
	return nil
}

func (r *RequirementRepo) GetProposal(ctx context.Context, id string) (*RequirementProposalRecord, error) {
	var rec RequirementProposalRecord
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRequirementNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get proposal: %w", err)
	}
	return &rec, nil
}

func (r *RequirementRepo) CountProposals(ctx context.Context, requirementID string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&RequirementProposalRecord{}).
		Where("requirement_id = ?", requirementID).Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("count proposals: %w", err)
	}
	return n, nil
}

// ---- jobs ----

func (r *RequirementRepo) EnqueueJob(ctx context.Context, rec *GroupJobRecord) error {
	if err := r.db.WithContext(ctx).Create(rec).Error; err != nil {
		return fmt.Errorf("enqueue job: %w", err)
	}
	return nil
}

func (r *RequirementRepo) GetJob(ctx context.Context, id string) (*GroupJobRecord, error) {
	var rec GroupJobRecord
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRequirementNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}
	return &rec, nil
}

func (r *RequirementRepo) GetJobForRequirement(ctx context.Context, requirementID string) (*GroupJobRecord, error) {
	var rec GroupJobRecord
	err := r.db.WithContext(ctx).Where("requirement_id = ?", requirementID).
		Order("created_at DESC, id DESC").First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRequirementNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get job for requirement: %w", err)
	}
	return &rec, nil
}

// NextQueuedJob returns the highest-priority due queued job, or
// ErrRequirementNotFound. Ordering: priority DESC, then FIFO.
func (r *RequirementRepo) NextQueuedJob(ctx context.Context, now time.Time) (*GroupJobRecord, error) {
	var rec GroupJobRecord
	err := r.db.WithContext(ctx).
		Where("state = ? AND available_at <= ?", "queued", now).
		Order("priority DESC, created_at ASC, id ASC").First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRequirementNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("next queued job: %w", err)
	}
	return &rec, nil
}

// ClaimJob atomically leases a queued job to owner until leaseUntil. The
// claim fails (returns false) when the job is not queued, another job with
// the same SerialKey is currently running under a live lease, or the CAS
// loses a race. Serialization is enforced HERE (not by the caller) so two
// workers can never run the same (group, assignee) concurrently.
func (r *RequirementRepo) ClaimJob(ctx context.Context, id, owner string, now, leaseUntil time.Time) (bool, error) {
	var job GroupJobRecord
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("claim job load: %w", err)
	}
	if job.State != "queued" {
		return false, nil
	}
	var running int64
	err := r.db.WithContext(ctx).Model(&GroupJobRecord{}).
		Where("serial_key = ? AND state = ? AND lease_expires_at > ? AND id <> ?",
			job.SerialKey, "running", now, id).
		Count(&running).Error
	if err != nil {
		return false, fmt.Errorf("claim job serial check: %w", err)
	}
	if running > 0 {
		return false, nil
	}
	res := r.db.WithContext(ctx).Model(&GroupJobRecord{}).
		Where("id = ? AND state = ?", id, "queued").
		Updates(map[string]any{
			"state": "running", "lease_owner": owner, "lease_expires_at": leaseUntil,
			"attempts": job.Attempts + 1, "updated_at": now,
		})
	if res.Error != nil {
		return false, fmt.Errorf("claim job: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// RequeueExpiredLeases returns running jobs whose lease has lapsed to the
// queued state (crash recovery). Jobs that already exhausted their attempts
// are failed instead, with their requirement transitioned by the caller via
// the returned rows.
func (r *RequirementRepo) RequeueExpiredLeases(ctx context.Context, now time.Time) ([]GroupJobRecord, error) {
	var expired []GroupJobRecord
	if err := r.db.WithContext(ctx).
		Where("state = ? AND lease_expires_at <= ?", "running", now).
		Find(&expired).Error; err != nil {
		return nil, fmt.Errorf("find expired leases: %w", err)
	}
	for i := range expired {
		job := &expired[i]
		if job.Attempts >= job.MaxAttempts {
			res := r.db.WithContext(ctx).Model(&GroupJobRecord{}).
				Where("id = ? AND state = ?", job.ID, "running").
				Updates(map[string]any{
					"state": "failed", "lease_owner": "", "lease_expires_at": nil,
					"last_error": "worker lease expired; attempts exhausted",
					"updated_at": now, "finished_at": now,
				})
			if res.Error != nil {
				return nil, fmt.Errorf("fail expired job: %w", res.Error)
			}
			job.State = "failed"
			continue
		}
		res := r.db.WithContext(ctx).Model(&GroupJobRecord{}).
			Where("id = ? AND state = ?", job.ID, "running").
			Updates(map[string]any{
				"state": "queued", "lease_owner": "", "lease_expires_at": nil,
				"available_at": now, "updated_at": now,
			})
		if res.Error != nil {
			return nil, fmt.Errorf("requeue expired job: %w", res.Error)
		}
		job.State = "queued"
	}
	return expired, nil
}

// CompleteJob marks a running job done (owner-checked).
func (r *RequirementRepo) CompleteJob(ctx context.Context, id, owner string, now time.Time) error {
	res := r.db.WithContext(ctx).Model(&GroupJobRecord{}).
		Where("id = ? AND state = ? AND lease_owner = ?", id, "running", owner).
		Updates(map[string]any{
			"state": "done", "lease_expires_at": nil, "updated_at": now, "finished_at": now,
		})
	if res.Error != nil {
		return fmt.Errorf("complete job: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrRequirementNotFound
	}
	return nil
}

// FailOrRetryJob either requeues a running job with backoff (attempts left)
// or marks it failed (attempts exhausted). Returns the resulting state.
func (r *RequirementRepo) FailOrRetryJob(ctx context.Context, id, owner string, now time.Time, backoff time.Duration, cause error) (string, error) {
	var out string
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job GroupJobRecord
		if err := tx.Where("id = ? AND state = ? AND lease_owner = ?", id, "running", owner).First(&job).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrRequirementNotFound
			}
			return err
		}
		updates := map[string]any{
			"lease_owner": "", "lease_expires_at": nil, "updated_at": now,
			"last_error": truncateString(cause.Error(), 2000),
		}
		if job.Attempts >= job.MaxAttempts {
			updates["state"] = "failed"
			updates["finished_at"] = now
			out = "failed"
		} else {
			updates["state"] = "queued"
			updates["available_at"] = now.Add(backoff)
			out = "queued"
		}
		return tx.Model(&GroupJobRecord{}).Where("id = ?", id).Updates(updates).Error
	})
	if err != nil {
		return "", fmt.Errorf("fail or retry job: %w", err)
	}
	return out, nil
}

// CancelQueuedJobsForRequirement cancels queued/running jobs of a
// requirement (user cancel). Running jobs observe the requirement state at
// their next transition point and stop without writing results.
func (r *RequirementRepo) CancelQueuedJobsForRequirement(ctx context.Context, requirementID string, now time.Time) error {
	res := r.db.WithContext(ctx).Model(&GroupJobRecord{}).
		Where("requirement_id = ? AND state IN ?", requirementID, []string{"queued", "running"}).
		Updates(map[string]any{
			"state": "cancelled", "lease_owner": "", "lease_expires_at": nil,
			"updated_at": now, "finished_at": now,
		})
	if res.Error != nil {
		return fmt.Errorf("cancel jobs for requirement: %w", res.Error)
	}
	return nil
}

// ---- invocations / spend ----

func (r *RequirementRepo) CreateInvocation(ctx context.Context, rec *GroupModelInvocationRecord) error {
	if err := r.db.WithContext(ctx).Create(rec).Error; err != nil {
		return fmt.Errorf("create invocation: %w", err)
	}
	return nil
}

// RequirementSpend is the aggregated ledger for one group, optionally
// restricted to one requirement.
type RequirementSpend struct {
	Invocations  int64
	InputTokens  int64
	OutputTokens int64
	CostMicros   int64
}

func (r *RequirementRepo) GroupSpend(ctx context.Context, groupID, requirementID string) (RequirementSpend, error) {
	var out RequirementSpend
	q := r.db.WithContext(ctx).Model(&GroupModelInvocationRecord{}).Where("group_id = ?", groupID)
	if requirementID != "" {
		q = q.Where("requirement_id = ?", requirementID)
	}
	err := q.Select("COUNT(*) AS invocations, COALESCE(SUM(input_tokens),0) AS input_tokens, COALESCE(SUM(output_tokens),0) AS output_tokens, COALESCE(SUM(estimated_cost_micros),0) AS cost_micros").
		Scan(&out).Error
	if err != nil {
		return out, fmt.Errorf("group spend: %w", err)
	}
	return out, nil
}
