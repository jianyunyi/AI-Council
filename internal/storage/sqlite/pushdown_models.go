package sqlite

import "time"

// Part B P8: red-team checks and requirement push-downs.
//
// A RedTeamCheckRecord is one red-team pass over one proposal attempt,
// bound to the plan hash the passing review round stamped. A
// RequirementPushdownRecord links one (requirement, proposal attempt)
// to the council task created in the assignee's model workspace after
// the D1 human approval; the unique indexes make a second approval or
// a duplicate task for the same attempt impossible at the storage
// layer. ID sizes follow the landed group tables (size:64).

// RedTeamCheckRecord statuses: queued|running|clear|flagged|error|superseded.
type RedTeamCheckRecord struct {
	ID              string     `gorm:"primaryKey;size:64"`
	GroupID         string     `gorm:"size:64;not null;index"`
	RequirementID   string     `gorm:"size:64;not null;index"`
	ProposalID      string     `gorm:"size:64;not null"`
	ProposalAttempt int        `gorm:"not null"`
	ReviewRoundID   string     `gorm:"size:64;not null"`
	PlanHash        string     `gorm:"size:64;not null"`
	Status          string     `gorm:"size:16;not null;index"` // queued|running|clear|flagged|error|superseded
	Independence    string     `gorm:"size:16;not null;default:'full'"` // full|degraded
	SeatProfileID   string     `gorm:"size:64;not null;default:''"`
	SeatModel       string     `gorm:"size:200;not null;default:''"`
	Report          []byte     `gorm:"type:blob"` // schema.RedTeamReport JSON
	Issues          []byte     `gorm:"type:blob"` // []council.PlanIssue JSON
	SeatFailures    []byte     `gorm:"type:blob"` // [{profile_id, model, error}]
	ErrorMsg        string     `gorm:"type:text;not null;default:''"`
	InvocationID    string     `gorm:"size:64;not null;default:''"`
	CreatedAt       time.Time  `gorm:"not null"`
	FinishedAt      *time.Time
}

func (RedTeamCheckRecord) TableName() string { return "redteam_checks" }

// RequirementPushdownRecord statuses:
// approved|task_created|task_approved|executing|succeeded|failed|cancelled.
type RequirementPushdownRecord struct {
	ID                string     `gorm:"primaryKey;size:64"`
	GroupID           string     `gorm:"size:64;not null;index"`
	RequirementID     string     `gorm:"size:64;not null;uniqueIndex:uq_pushdown_attempt,priority:1"`
	ProposalAttempt   int        `gorm:"not null;uniqueIndex:uq_pushdown_attempt,priority:2"`
	ProposalID        string     `gorm:"size:64;not null"`
	ReviewRoundID     string     `gorm:"size:64;not null"`
	RedTeamCheckID    string     `gorm:"size:64;not null"`
	PlanHash          string     `gorm:"size:64;not null"`
	IdempotencyKey    string     `gorm:"size:120;not null;uniqueIndex:uq_pushdown_idem"`
	ApprovedBy        string     `gorm:"size:64;not null"`
	ApprovedAt        time.Time  `gorm:"not null"`
	ModelWorkspaceID  string     `gorm:"size:64;not null"`
	TaskID            *string    `gorm:"size:64;uniqueIndex:uq_pushdown_task"`
	ApprovalID        *string    `gorm:"size:64;uniqueIndex:uq_pushdown_approval"`
	Status            string     `gorm:"size:20;not null;index"` // approved|task_created|task_approved|executing|succeeded|failed|cancelled
	LastMirroredState string     `gorm:"size:64;not null;default:''"` // "<task state>/<stage>" of the last mirrored status
	LastError         string     `gorm:"type:text;not null;default:''"`
	FailureCategory   string     `gorm:"size:48;not null;default:''"`
	Attempts          int        `gorm:"not null;default:0"`
	UpdatedAt         time.Time  `gorm:"not null"`
	FinishedAt        *time.Time
}

func (RequirementPushdownRecord) TableName() string { return "requirement_pushdowns" }
