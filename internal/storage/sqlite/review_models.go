package sqlite

import (
	"errors"
	"time"
)

// Part B P7 review rounds: one round reviews exactly one proposal of a
// requirement. Decisions are blind while the round is open — the service
// layer withholds verdict/rationale until the round closes (the records
// themselves always store them; see app/group buildReviewState).

var ErrReviewNotFound = errors.New("review: not found")

type ReviewRoundRecord struct {
	ID               string     `gorm:"primaryKey;size:64"`
	GroupID          string     `gorm:"size:64;index:idx_review_rounds_group,priority:1"`
	RequirementID    string     `gorm:"size:64;index:idx_review_rounds_req,priority:1"`
	ProposalID       string     `gorm:"size:64"`
	ProposalRound    int        // == proposal Attempt (1-based)
	PlanHash         string     `gorm:"size:64"` // sha256 hex of the proposal's PlanJSON at open
	Status           string     `gorm:"size:16;index:idx_review_rounds_req,priority:2"` // open|closed
	Outcome          string     `gorm:"size:24"`                                        // ""|passed|changes_requested|rejected|inconclusive|superseded
	PolicyRule       string     `gorm:"size:16"`                                        // snapshot: unanimous|quorum
	PolicyQuorum     int
	StartedByUserID  string    `gorm:"size:64"`
	StartedByMember  string    `gorm:"size:64"`
	RequestMessageID string    `gorm:"size:64"`
	DeadlineAt       *time.Time // stored + displayed only in v1; no auto-close
	RevisionNote     string     `gorm:"type:text"` // set by ReviseRequirement on a changes_requested round
	CreatedAt        time.Time
	ClosedAt         *time.Time
	ClosedBy         string `gorm:"size:64"` // user id, or "system"
}

type ReviewDecisionRecord struct {
	ID              string `gorm:"primaryKey;size:64"`
	RoundID         string `gorm:"size:64;index:idx_review_decisions_round,priority:1;uniqueIndex:idx_review_decisions_member,priority:1"`
	GroupID         string `gorm:"size:64"`
	RequirementID   string `gorm:"size:64;index"`
	ReviewerMember  string `gorm:"size:64;uniqueIndex:idx_review_decisions_member,priority:2"`
	ReviewerType    string `gorm:"size:8"` // user|ai
	ReviewerAIModel string `gorm:"size:64"`
	Status          string `gorm:"size:16;index:idx_review_decisions_round,priority:2"` // pending|running|decided|error|withdrawn
	Verdict         string `gorm:"size:24"`                                             // ""|approve|request_changes|reject
	Rationale       string `gorm:"type:text"`
	JobID           string `gorm:"size:64"`
	InvocationID    string `gorm:"size:64"`
	Error           string `gorm:"type:text"`
	CreatedAt       time.Time
	DecidedAt       *time.Time
	UpdatedAt       time.Time
}
