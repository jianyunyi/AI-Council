package sqlite

import "time"

// Part B P6: requirements dispatched to AI members, their proposals, the
// persistent job queue that drives the proposal pipeline, and the model
// invocation ledger used for budget accounting. Requirement/proposal state
// strings live in internal/app/group (requirement_ports.go); the storage
// layer stays stringly typed like the P5 group tables.

// GroupRequirementRecord is one dispatched requirement: a human's @dispatch
// to exactly one AI member (fan-out creates one row per AI). State machine:
// dispatched -> proposing -> proposed | proposal_failed | failed |
// cancelled (P6 terminal states; review/execution states arrive in P7+).
type GroupRequirementRecord struct {
	ID                  string     `gorm:"primaryKey;size:64"`
	GroupID             string     `gorm:"size:64;not null;index:idx_req_group_state,priority:1"`
	AnchorMessageID     string     `gorm:"size:64;not null;index"`
	RequestedByUserID   string     `gorm:"size:128;not null;index"`
	RequestedByMemberID string     `gorm:"size:64;not null"`
	AssigneeMemberID    string     `gorm:"size:64;not null;index"`
	AssigneeAIModelID   string     `gorm:"size:64;not null;index"`
	AssigneeHandle      string     `gorm:"size:64;not null"`
	Title               string     `gorm:"size:200;not null"`
	BodyMD              string     `gorm:"type:text;not null"`
	AcceptanceJSON      []byte     `gorm:"type:blob"`
	State               string     `gorm:"size:32;not null;index:idx_req_group_state,priority:2"`
	CurrentProposalID   string     `gorm:"size:64"`
	FailureCode         string     `gorm:"size:64"`
	FailureDetail       string     `gorm:"type:text"`
	Version             int        `gorm:"not null;default:1"`
	CancelledByUserID   string     `gorm:"size:128"`
	CancelledByMemberID string     `gorm:"size:64"`
	CancelReason        string     `gorm:"type:text"`
	CreatedAt           time.Time  `gorm:"not null"`
	UpdatedAt           time.Time  `gorm:"not null"`
	StateChangedAt      time.Time  `gorm:"not null"`
	ProposedAt          *time.Time `gorm:"index"`
	TerminalAt          *time.Time
}

func (GroupRequirementRecord) TableName() string { return "group_requirements" }

// RequirementProposalRecord is one concrete plan an assignee model produced
// for a requirement. PlanJSON is schema.ExecutionPlan; RawResponseJSON is
// the model's unparsed reply (kept for the P6 repair loop and for audit).
// (requirement_id, attempt) is unique: retries create new attempts.
type RequirementProposalRecord struct {
	ID                string    `gorm:"primaryKey;size:64"`
	RequirementID     string    `gorm:"size:64;not null;uniqueIndex:idx_proposal_attempt,priority:1;index"`
	GroupID           string    `gorm:"size:64;not null;index"`
	Attempt           int       `gorm:"not null;uniqueIndex:idx_proposal_attempt,priority:2"`
	PlanJSON          []byte    `gorm:"type:blob"`
	PlanVersion       int       `gorm:"not null;default:0"`
	Summary           string    `gorm:"type:text"`
	RawResponseJSON   []byte    `gorm:"type:blob"`
	ModelInvocationID string    `gorm:"size:64;index"`
	CreatedByMemberID string    `gorm:"size:64;not null"`
	CreatedAt         time.Time `gorm:"not null"`
}

func (RequirementProposalRecord) TableName() string { return "requirement_proposals" }

// GroupJobRecord is the persistent work queue row. One job = one pipeline
// run for one requirement (kind "propose" in P6). Workers claim rows with
// a lease (ClaimJob); an expired lease makes the row claimable again, so a
// crashed worker never strands a requirement. SerialKey serializes work:
// at most one running job per (group, assignee) in P6.
type GroupJobRecord struct {
	ID             string     `gorm:"primaryKey;size:64"`
	GroupID        string     `gorm:"size:64;not null;index:idx_job_claim,priority:1"`
	RequirementID  string     `gorm:"size:64;not null;index"`
	Kind           string     `gorm:"size:32;not null;index"`
	TargetID       string     `gorm:"size:64"` // P7: review jobs carry the decision id
	State          string     `gorm:"size:16;not null;index:idx_job_claim,priority:2"` // queued|running|done|failed|cancelled
	SerialKey      string     `gorm:"size:128;not null;index"`
	Priority       int        `gorm:"not null;default:0"`
	Attempts       int        `gorm:"not null;default:0"`
	MaxAttempts    int        `gorm:"not null;default:3"`
	AvailableAt    time.Time  `gorm:"not null;index:idx_job_claim,priority:3"`
	LeaseOwner     string     `gorm:"size:64"`
	LeaseExpiresAt *time.Time `gorm:"index"`
	LastError      string     `gorm:"type:text"`
	CreatedAt      time.Time  `gorm:"not null"`
	UpdatedAt      time.Time  `gorm:"not null"`
	FinishedAt     *time.Time
}

func (GroupJobRecord) TableName() string { return "group_jobs" }

// GroupModelInvocationRecord is the budget ledger: one row per provider
// call made on behalf of a group (proposal generation and repair in P6).
// EstimatedCostMicros is filled from the pricing table when known; token
// counts always are. Group spend = SUM over non-orphaned rows.
type GroupModelInvocationRecord struct {
	ID                  string    `gorm:"primaryKey;size:64"`
	GroupID             string    `gorm:"size:64;not null;index:idx_inv_group_created,priority:1"`
	RequirementID       string    `gorm:"size:64;index"`
	JobID               string    `gorm:"size:64;index"`
	AIModelID           string    `gorm:"size:64;not null;index"`
	ProviderProfileID   string    `gorm:"size:64"`
	Model               string    `gorm:"size:200"`
	Purpose             string    `gorm:"size:32;not null;index"` // propose|propose_repair
	Status              string    `gorm:"size:16;not null"`       // ok|error
	InputTokens         int64     `gorm:"not null;default:0"`
	OutputTokens        int64     `gorm:"not null;default:0"`
	EstimatedCostMicros int64     `gorm:"not null;default:0"`
	LatencyMS           int64     `gorm:"not null;default:0"`
	Error               string    `gorm:"type:text"`
	CreatedAt           time.Time `gorm:"not null;index:idx_inv_group_created,priority:2"`
}

func (GroupModelInvocationRecord) TableName() string { return "group_model_invocations" }
