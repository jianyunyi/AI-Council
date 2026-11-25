package sqlite

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// Sentinel errors for the P9 store, following the ErrReview*/ErrPushdown*
// convention of the sibling repos.
var (
	ErrSubmissionNotFound    = errors.New("result submission not found")
	ErrGroupArtifactNotFound = errors.New("group artifact not found")
)

// ResultRepo persists P9 result submissions and the group artifact
// manifest. Like RequirementRepo/ReviewRepo it is constructed over any
// *gorm.DB — the request-scoped handle, or tx.DB() inside a GroupRepo.Tx
// so a submission, its manifest rows and its events commit atomically.
type ResultRepo struct{ db *gorm.DB }

func NewResultRepo(db *gorm.DB) *ResultRepo { return &ResultRepo{db: db} }

func (r *ResultRepo) DB() *gorm.DB { return r.db }

func (r *ResultRepo) CreateSubmission(ctx context.Context, s *ResultSubmissionRecord) error {
	return r.db.WithContext(ctx).Create(s).Error
}

func (r *ResultRepo) GetSubmission(ctx context.Context, id string) (*ResultSubmissionRecord, error) {
	var s ResultSubmissionRecord
	err := r.db.WithContext(ctx).Where("id = ?", id).Take(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrSubmissionNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// LatestSubmission returns the newest submission of a requirement, or (nil, nil).
func (r *ResultRepo) LatestSubmission(ctx context.Context, requirementID string) (*ResultSubmissionRecord, error) {
	var s ResultSubmissionRecord
	err := r.db.WithContext(ctx).Where("requirement_id = ?", requirementID).
		Order("cycle DESC, created_at DESC").Take(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *ResultRepo) ListSubmissions(ctx context.Context, requirementID string) ([]ResultSubmissionRecord, error) {
	var out []ResultSubmissionRecord
	err := r.db.WithContext(ctx).Where("requirement_id = ?", requirementID).Order("cycle ASC, created_at ASC").Find(&out).Error
	return out, err
}

// TransitionSubmission is a CAS on status; true only if this caller won.
func (r *ResultRepo) TransitionSubmission(ctx context.Context, id string, from []string, fields map[string]any) (bool, error) {
	res := r.db.WithContext(ctx).Model(&ResultSubmissionRecord{}).Where("id = ? AND status IN ?", id, from).Updates(fields)
	return res.RowsAffected == 1, res.Error
}

// FindArtifact returns (nil, nil) when the source artifact has no manifest row yet.
func (r *ResultRepo) FindArtifact(ctx context.Context, submissionID, sourceID string) (*GroupArtifactRecord, error) {
	var a GroupArtifactRecord
	err := r.db.WithContext(ctx).Where("submission_id = ? AND source_artifact_id = ?", submissionID, sourceID).Take(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *ResultRepo) CreateArtifact(ctx context.Context, a *GroupArtifactRecord) error {
	return r.db.WithContext(ctx).Create(a).Error
}

func (r *ResultRepo) ListArtifactsForSubmission(ctx context.Context, submissionID string) ([]GroupArtifactRecord, error) {
	var out []GroupArtifactRecord
	err := r.db.WithContext(ctx).Where("submission_id = ?", submissionID).Order("name ASC, id ASC").Find(&out).Error
	return out, err
}

func (r *ResultRepo) ListArtifactsForRequirement(ctx context.Context, groupID, requirementID string) ([]GroupArtifactRecord, error) {
	var out []GroupArtifactRecord
	err := r.db.WithContext(ctx).Where("group_id = ? AND requirement_id = ?", groupID, requirementID).
		Order("cycle ASC, name ASC, id ASC").Find(&out).Error
	return out, err
}

// GetGroupArtifact is scoped by group AND requirement: the manifest rule on reads.
func (r *ResultRepo) GetGroupArtifact(ctx context.Context, groupID, requirementID, id string) (*GroupArtifactRecord, error) {
	var a GroupArtifactRecord
	err := r.db.WithContext(ctx).Where("id = ? AND group_id = ? AND requirement_id = ?", id, groupID, requirementID).Take(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrGroupArtifactNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *ResultRepo) SumCopiedBytes(ctx context.Context, submissionID string) (int64, int64, error) {
	var row struct{ N, Bytes int64 }
	err := r.db.WithContext(ctx).Model(&GroupArtifactRecord{}).
		Select("COUNT(*) AS n, COALESCE(SUM(size_bytes),0) AS bytes").
		Where("submission_id = ? AND status = ?", submissionID, "copied").Scan(&row).Error
	return row.N, row.Bytes, err
}

func (r *ResultRepo) UpdateArtifact(ctx context.Context, id string, fields map[string]any) error {
	return r.db.WithContext(ctx).Model(&GroupArtifactRecord{}).Where("id = ?", id).Updates(fields).Error
}
