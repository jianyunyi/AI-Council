package sqlite

import "time"

type RunRecord struct {
	ID        string `gorm:"primaryKey;size:64"`
	State     string `gorm:"not null;size:32"`
	Version   uint64 `gorm:"not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

type AuditRecord struct {
	ID        string `gorm:"primaryKey;size:64"`
	RunID     string `gorm:"not null;uniqueIndex:idx_audit_run_sequence,priority:1;index"`
	Sequence  uint64 `gorm:"not null;uniqueIndex:idx_audit_run_sequence,priority:2"`
	Type      string `gorm:"not null;size:64"`
	Actor     string `gorm:"not null;size:128"`
	Detail    string `gorm:"not null"`
	CreatedAt time.Time
}

type ArtifactRecord struct {
	ID            string `gorm:"primaryKey;size:64"`
	RunID         string `gorm:"not null;index"`
	Type          string `gorm:"not null;size:64"`
	SchemaVersion string `gorm:"not null;size:16"`
	ParentID      string `gorm:"size:64"`
	ContentHash   string `gorm:"not null;size:64"`
	FilePath      string `gorm:"not null"`
	CreatedAt     time.Time
}

type ProviderProfileRecord struct {
	ID             string `gorm:"primaryKey;size:64"`
	Provider       string
	Model          string
	ParametersJSON []byte
}
type WorkspaceRecord struct {
	ID            string `gorm:"primaryKey;size:64"`
	CanonicalRoot string
	RunnerID      string
	IsGit         bool
	Dirty         bool
	PolicyJSON    []byte
}
type TaskRecord struct {
	ID               string `gorm:"primaryKey;size:64"`
	WorkspaceID      string
	Requirement      string
	ConstraintsJSON  []byte
	AcceptanceJSON   []byte
	State            string
	PlanVersion      int
	ApprovalHash     string
	ApprovalGranted  bool
	PlanJSON         []byte
	VerificationJSON []byte
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
type SeatRecord struct {
	ID                string `gorm:"primaryKey;size:64"`
	RunID             string `gorm:"index"`
	ProviderProfileID string
	Role              string
	ProposalAlias     string
}
type ModelInvocationRecord struct {
	ID                  string `gorm:"primaryKey;size:64"`
	RunID               string `gorm:"index"`
	SeatID              string
	Stage               string
	ProviderRequestID   string
	InputTokens         int64
	OutputTokens        int64
	EstimatedCostMicros int64
	DurationMillis      int64
	ErrorCode           string
}

type ExecutionRecord struct {
	RequestID    string `gorm:"primaryKey;size:128"`
	Status       string `gorm:"size:16;index"`
	ResponseJSON []byte `gorm:"not null"`
	CreatedAt    time.Time
}
type UserRecord struct {
	ID           string `gorm:"primaryKey;size:64"`
	Subject      string `gorm:"uniqueIndex;size:128"`
	TokenHash    string `gorm:"size:128"`
	PasswordHash *string `gorm:"size:255"`
	Disabled     bool
	CreatedAt    time.Time
}
type RoleRecord struct {
	ID   string `gorm:"primaryKey;size:64"`
	Name string `gorm:"uniqueIndex;size:64"`
}
type UserRoleRecord struct {
	UserID string `gorm:"primaryKey;size:64"`
	RoleID string `gorm:"primaryKey;size:64"`
}

type PermissionRecord struct {
	ID   string `gorm:"primaryKey;size:128"`
	Name string `gorm:"uniqueIndex;size:128;not null"`
}

type RolePermissionRecord struct {
	RoleID      string `gorm:"primaryKey;size:64"`
	PermissionID string `gorm:"primaryKey;size:128"`
}

type AccessTokenRecord struct {
	ID        string `gorm:"primaryKey;size:64"`
	UserID    string `gorm:"not null;size:64;index"`
	TokenHash string `gorm:"not null;uniqueIndex;size:64"`
	ExpiresAt time.Time `gorm:"index"`
	RevokedAt *time.Time
	CreatedAt time.Time
}
