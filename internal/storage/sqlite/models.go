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
	OwnerUserID   string `gorm:"size:64;index"`
	RunID         string `gorm:"not null;index"`
	Type          string `gorm:"not null;size:64"`
	SchemaVersion string `gorm:"not null;size:16"`
	ParentID      string `gorm:"size:64"`
	ContentHash   string `gorm:"not null;size:64"`
	FilePath      string `gorm:"not null"`
	CreatedAt     time.Time
}

type ProviderProfileRecord struct {
	ID               string `gorm:"primaryKey;size:64"`
	OwnerUserID      string `gorm:"size:64;index"`
	Provider         string `gorm:"not null;size:32"`
	Model            string `gorm:"not null;size:128"`
	APIKeyCiphertext []byte
	ParametersJSON   []byte
	CreatedAt        time.Time
}
type WorkspaceRecord struct {
	ID            string `gorm:"primaryKey;size:64"`
	OwnerUserID   string `gorm:"size:64;index"`
	CanonicalRoot string
	RunnerPath    string
	RunnerID      string
	IsGit         bool
	Dirty         bool
	PolicyJSON    []byte
}
type WorkspaceShareRecord struct {
	WorkspaceID string    `gorm:"primaryKey;size:64;uniqueIndex:idx_workspace_share_user,priority:1;index"`
	UserID      string    `gorm:"primaryKey;size:64;uniqueIndex:idx_workspace_share_user,priority:2;index"`
	Access      string    `gorm:"not null;size:16"`
	GrantedBy   string    `gorm:"not null;size:64"`
	CreatedAt   time.Time `gorm:"not null"`
	UpdatedAt   time.Time `gorm:"not null"`
}
type RunnerWorkspaceRecord struct {
	WorkspaceID   string    `gorm:"primaryKey;size:64"`
	CanonicalRoot string    `gorm:"not null;uniqueIndex"`
	CreatedAt     time.Time `gorm:"not null"`
}
type TaskRecord struct {
	ID               string `gorm:"primaryKey;size:64"`
	OwnerUserID      string `gorm:"size:64;index"`
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
	UpdatedAt    time.Time
}
type UserRecord struct {
	ID              string  `gorm:"primaryKey;size:64"`
	Subject         string  `gorm:"uniqueIndex;size:128"`
	Email           *string `gorm:"uniqueIndex;size:254"`
	EmailVerifiedAt *time.Time
	TokenHash       string  `gorm:"size:128"`
	PasswordHash    *string `gorm:"size:255"`
	Disabled        bool
	CreatedAt       time.Time
}

type IdentityTokenRecord struct {
	ID         string    `gorm:"primaryKey;size:64"`
	UserID     string    `gorm:"not null;size:64;index"`
	Purpose    string    `gorm:"not null;size:32;index"`
	TokenHash  string    `gorm:"not null;uniqueIndex;size:64"`
	ExpiresAt  time.Time `gorm:"not null;index"`
	ConsumedAt *time.Time
	CreatedAt  time.Time
}

type OAuthFlowRecord struct {
	StateHash    string    `gorm:"primaryKey;size:64"`
	Provider     string    `gorm:"not null;size:32"`
	NonceHash    string    `gorm:"not null;size:64"`
	VerifierHash string    `gorm:"not null;size:64"`
	ExpiresAt    time.Time `gorm:"not null;index"`
	ConsumedAt   *time.Time
	CreatedAt    time.Time
}

type OAuthIdentityRecord struct {
	ID              string `gorm:"primaryKey;size:64"`
	UserID          string `gorm:"not null;size:64;index"`
	Provider        string `gorm:"not null;size:32;uniqueIndex:idx_oauth_identity_provider_subject,priority:1"`
	ProviderSubject string `gorm:"not null;size:255;uniqueIndex:idx_oauth_identity_provider_subject,priority:2"`
	CreatedAt       time.Time
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
	RoleID       string `gorm:"primaryKey;size:64"`
	PermissionID string `gorm:"primaryKey;size:128"`
}

type AccessTokenRecord struct {
	ID        string    `gorm:"primaryKey;size:64"`
	UserID    string    `gorm:"not null;size:64;index"`
	TokenHash string    `gorm:"not null;uniqueIndex;size:64"`
	ExpiresAt time.Time `gorm:"index"`
	RevokedAt *time.Time
	CreatedAt time.Time
}

// ApprovalNonceRecord records each one-time approval nonce the Runner has
// consumed, so a signed execution request cannot be replayed with a new
// request_id. The unique primary key is the fail-closed replay guard.
type ApprovalNonceRecord struct {
	Nonce      string    `gorm:"primaryKey;size:128"`
	ConsumedAt time.Time `gorm:"not null"`
}
