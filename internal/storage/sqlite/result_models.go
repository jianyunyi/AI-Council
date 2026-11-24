package sqlite

import "time"

// Part B P9: result submissions and the per-group artifact manifest.
// One submission per push-down (one execution cycle): the result job
// copies the task's allowed artifacts into the group's shared
// workspace, has the assignee model summarize, and commits the
// submission, which a stage="result" review round then judges.

// ResultSubmissionRecord is the result of one push-down (one execution cycle).
type ResultSubmissionRecord struct {
	ID               string     `gorm:"primaryKey;size:40"`
	GroupID          string     `gorm:"size:40;not null;index"`
	RequirementID    string     `gorm:"size:40;not null;index"`
	PushdownID       string     `gorm:"size:40;not null;uniqueIndex:uq_result_pushdown"`
	Cycle            int        `gorm:"not null"`
	ProposalAttempt  int        `gorm:"not null"`
	TaskID           string     `gorm:"size:64;not null;default:''"`
	ExecutionOutcome string     `gorm:"size:16;not null"` // succeeded|failed|cancelled|not_executed
	FailureCategory  string     `gorm:"size:48;not null;default:''"`
	Status           string     `gorm:"size:16;not null;index"` // collecting|summarizing|submitted
	SummaryMD        string     `gorm:"type:text;not null;default:''"`
	SummarySource    string     `gorm:"size:8;not null;default:''"` // model|facts
	AcceptanceReport []byte     `gorm:"type:blob"`                  // []AcceptanceReportItem (app/group JSON)
	ResultHash       string     `gorm:"size:64;not null;default:''"`
	InvocationID     string     `gorm:"size:40;not null;default:''"`
	MessageID        string     `gorm:"size:40;not null;default:''"`
	CreatedAt        time.Time  `gorm:"not null"`
	SubmittedAt      *time.Time
}

func (ResultSubmissionRecord) TableName() string { return "result_submissions" }

// GroupArtifactRecord is the per-group manifest: the ONLY way a group can see an artifact.
// One row per (submission, source artifact); copies live in the group's shared workspace.
type GroupArtifactRecord struct {
	ID               string    `gorm:"primaryKey;size:40"`
	GroupID          string    `gorm:"size:40;not null;index"`
	RequirementID    string    `gorm:"size:40;not null;index"`
	SubmissionID     string    `gorm:"size:40;not null;uniqueIndex:uq_group_artifact_src,priority:1"`
	SourceArtifactID string    `gorm:"size:64;not null;uniqueIndex:uq_group_artifact_src,priority:2"`
	Cycle            int       `gorm:"not null"`
	Name             string    `gorm:"size:255;not null"`
	Kind             string    `gorm:"size:24;not null"`
	MediaType        string    `gorm:"size:100;not null;default:''"`
	SizeBytes        int64     `gorm:"not null;default:0"`
	SHA256           string    `gorm:"size:64;not null;default:''"`
	CopyRef          string    `gorm:"size:255;not null;default:''"`
	Status           string    `gorm:"size:20;not null"` // copied|excluded_kind|too_large|limit_reached|digest_missing|digest_mismatch|error
	Detail           string    `gorm:"size:500;not null;default:''"`
	CreatedAt        time.Time `gorm:"not null"`
}

func (GroupArtifactRecord) TableName() string { return "group_artifacts" }
