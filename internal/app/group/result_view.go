package group

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// Part B P9 read surface: submission views (the requirement view
// embeds the latest), the per-cycle results list, and manifest-checked
// artifact downloads. The manifest rule is absolute: a group only ever
// sees artifacts of its own requirements' tasks, and a download must
// name a manifest row of THIS group AND requirement with status
// copied — anything else is 404.

type GroupArtifactView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	MediaType string `json:"media_type,omitempty"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256,omitempty"`
	Status    string `json:"status"`
	Cycle     int    `json:"cycle"`
}

type ResultSubmissionView struct {
	ID               string                 `json:"id"`
	Cycle            int                    `json:"cycle"`
	ProposalAttempt  int                    `json:"proposal_attempt"`
	TaskID           string                 `json:"task_id,omitempty"`
	ExecutionOutcome string                 `json:"execution_outcome"`
	FailureCategory  string                 `json:"failure_category,omitempty"`
	Status           string                 `json:"status"`
	SummaryMD        string                 `json:"summary_md,omitempty"`
	SummarySource    string                 `json:"summary_source,omitempty"`
	AcceptanceReport []AcceptanceReportItem `json:"acceptance_report,omitempty"`
	ResultHash       string                 `json:"result_hash,omitempty"`
	Artifacts        []GroupArtifactView    `json:"artifacts"`
	SubmittedAt      *time.Time             `json:"submitted_at,omitempty"`
}

func artifactView(a sqlite.GroupArtifactRecord) GroupArtifactView {
	return GroupArtifactView{ID: a.ID, Name: a.Name, Kind: a.Kind, MediaType: a.MediaType, SizeBytes: a.SizeBytes,
		SHA256: a.SHA256, Status: a.Status, Cycle: a.Cycle}
}

func (s *Service) submissionView(ctx context.Context, sub *sqlite.ResultSubmissionRecord) (*ResultSubmissionView, error) {
	arts, err := s.results().ListArtifactsForSubmission(ctx, sub.ID)
	if err != nil {
		return nil, err
	}
	v := &ResultSubmissionView{ID: sub.ID, Cycle: sub.Cycle, ProposalAttempt: sub.ProposalAttempt, TaskID: sub.TaskID,
		ExecutionOutcome: sub.ExecutionOutcome, FailureCategory: sub.FailureCategory, Status: sub.Status,
		SummaryMD: sub.SummaryMD, SummarySource: sub.SummarySource, ResultHash: sub.ResultHash,
		Artifacts: make([]GroupArtifactView, 0, len(arts)), SubmittedAt: sub.SubmittedAt}
	_ = json.Unmarshal(sub.AcceptanceReport, &v.AcceptanceReport)
	for _, a := range arts {
		v.Artifacts = append(v.Artifacts, artifactView(a))
	}
	return v, nil
}

func (s *Service) ListResults(ctx context.Context, a Actor, gid, rid string) ([]ResultSubmissionView, error) {
	if _, _, err := s.access(ctx, a, gid); err != nil {
		return nil, err // non-members: 404
	}
	if _, err := s.reqRepo.GetRequirementInGroup(ctx, gid, rid); err != nil {
		return nil, errRequirementMissing
	}
	subs, err := s.results().ListSubmissions(ctx, rid)
	if err != nil {
		return nil, err
	}
	out := make([]ResultSubmissionView, 0, len(subs))
	for i := range subs {
		v, err := s.submissionView(ctx, &subs[i])
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}

// OpenArtifact streams a COPY from the group's shared workspace. The manifest row must
// belong to this group and requirement and be status=copied; anything else is 404.
func (s *Service) OpenArtifact(ctx context.Context, a Actor, gid, rid, aid string) (*GroupArtifactView, io.ReadCloser, error) {
	if !s.resultsEnabled() {
		return nil, nil, errResultsDisabled
	}
	if _, _, err := s.access(ctx, a, gid); err != nil {
		return nil, nil, err
	}
	rec, err := s.results().GetGroupArtifact(ctx, gid, rid, aid)
	if errors.Is(err, sqlite.ErrGroupArtifactNotFound) || (err == nil && (rec.Status != "copied" || rec.CopyRef == "")) {
		return nil, nil, errArtifactNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	rc, err := s.artifacts.Open(ctx, rec.CopyRef)
	if err != nil {
		return nil, nil, err
	}
	v := artifactView(*rec)
	s.record(ctx, a.UserID, "group.artifact_downloaded", gid, "group_artifact", aid, nil)
	return &v, rc, nil
}
