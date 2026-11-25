package sqlite

import "time"

type RunRecord struct {
	ID                string `gorm:"primaryKey;size:64"`
	State             string `gorm:"not null;size:32"`
	Version           uint64 `gorm:"not null"`
	CouncilConfigJSON []byte
	CreatedAt         time.Time
	UpdatedAt         time.Time
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
	// Kind: "standard" | "model_private" | "group_shared". See internal/app/wskind.
	Kind string `gorm:"size:16;not null;default:standard;index"`
	// Archived hides the workspace from API listings without deleting it
	// (used when an AI model is disabled; reactivation unarchives).
	Archived bool `gorm:"not null;default:false"`
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
	WorkspaceID      string `gorm:"index:idx_tasks_workspace_updated,priority:1"`
	Title            string `gorm:"size:255"`
	CreatedBy        string `gorm:"size:64;index:idx_tasks_creator_updated,priority:1"`
	Origin           string `gorm:"size:32;index;not null;default:direct"`
	// ExternalKey is the P8 idempotency key for group push-down tasks
	// ("pd:<requirement>:<attempt>"); NULL for direct tasks. SQLite
	// unique indexes treat NULLs as distinct, so direct tasks (NULL)
	// never collide.
	ExternalKey *string `gorm:"size:120;uniqueIndex:uq_tasks_external_key"`
	// GroupRequirementID back-links a push-down task to its group
	// requirement ("" for direct tasks).
	GroupRequirementID string `gorm:"size:64;not null;default:'';index"`
	Requirement        string
	ConstraintsJSON  []byte
	AcceptanceJSON   []byte
	State            string `gorm:"index:idx_tasks_state_updated,priority:1"`
	PlanVersion      int
	ApprovalHash     string
	ApprovalGranted  bool
	PlanJSON         []byte
	VerificationJSON []byte
	CreatedAt        time.Time
	UpdatedAt        time.Time `gorm:"index:idx_tasks_workspace_updated,priority:2;index:idx_tasks_state_updated,priority:2;index:idx_tasks_creator_updated,priority:2"`
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

// TaskRejectionRecord is the durable record of a plan rejection: who rejected
// which plan version, why (category + free-text reason), and when. Rows are
// append-only; the task detail reads the newest 20 and the task list reads
// the newest one per task.
type TaskRejectionRecord struct {
	ID              string    `gorm:"primaryKey;size:64"`
	TaskID          string    `gorm:"not null;size:64;index:idx_task_rejections_task_created,priority:1"`
	RunID           string    `gorm:"not null;size:64;index"`
	PlanVersion     int       `gorm:"not null"`
	ApprovalNonceID *string   `gorm:"size:128"`
	PlanHash        string    `gorm:"size:128"`
	RejectedBy      string    `gorm:"not null;size:64"`
	Category        string    `gorm:"not null;size:32"`
	Reason          string    `gorm:"not null"`
	Source          string    `gorm:"not null;size:32;default:task"`
	CreatedAt       time.Time `gorm:"index:idx_task_rejections_task_created,priority:2"`
}

// CouncilRoleAssignmentRecord is one council role slot: which provider
// profile + model fills a role (proposer/reviewer/judge/red_team) at a given
// ordinal within a scope. Scopes are per account: the global set
// (scope_id = "") and one optional overriding set per workspace.
type CouncilRoleAssignmentRecord struct {
	ID                string `gorm:"primaryKey;size:64"`
	OwnerUserID       string `gorm:"not null;size:64;uniqueIndex:idx_council_role_slot,priority:1"`
	ScopeType         string `gorm:"not null;size:16;uniqueIndex:idx_council_role_slot,priority:2"`
	ScopeID           string `gorm:"not null;size:64;uniqueIndex:idx_council_role_slot,priority:3"`
	Role              string `gorm:"not null;size:32;uniqueIndex:idx_council_role_slot,priority:4"`
	Ordinal           int    `gorm:"not null;uniqueIndex:idx_council_role_slot,priority:5"`
	ProviderProfileID string `gorm:"not null;size:64"`
	Model             string `gorm:"not null;size:128"`
	ParamsJSON        []byte
	Enabled           bool   `gorm:"not null;default:true"`
	UpdatedBy         string `gorm:"not null;size:64"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// CouncilRoleAssignmentSetRecord carries the optimistic-concurrency version
// of one scope's assignment set. A scope with no row has no stored set:
// resolution falls through to the next scope (or the legacy default).
type CouncilRoleAssignmentSetRecord struct {
	OwnerUserID string `gorm:"primaryKey;size:64"`
	ScopeType   string `gorm:"primaryKey;size:16"`
	ScopeID     string `gorm:"primaryKey;size:64"`
	Version     int    `gorm:"not null"`
	UpdatedBy   string `gorm:"not null;size:64"`
	UpdatedAt   time.Time
}

// AIModelRecord is an admin-enabled (provider profile, model) pair with a
// mention handle. The handle is globally unique: group chats address models
// by @handle, so it must be unambiguous across the deployment.
type AIModelRecord struct {
	ID                string `gorm:"primaryKey;size:64"`
	ProviderProfileID string `gorm:"size:64;not null;uniqueIndex:uq_ai_models_profile_model,priority:1"`
	Model             string `gorm:"size:200;not null;uniqueIndex:uq_ai_models_profile_model,priority:2"`
	Handle            string `gorm:"size:32;not null;uniqueIndex:uq_ai_models_handle"`
	DisplayName       string `gorm:"size:120;not null"`
	DefaultParamsJSON []byte
	Status            string `gorm:"size:16;not null;index"` // active|disabled|degraded
	CreatedBy         string `gorm:"size:64;not null"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (AIModelRecord) TableName() string { return "ai_models" }

// ModelWorkspaceRecord binds an AI model to its private workspace and tracks
// Runner registration for that workspace.
type ModelWorkspaceRecord struct {
	AIModelID string `gorm:"primaryKey;size:64"`
	WorkspaceID string `gorm:"size:64;not null;uniqueIndex:uq_model_workspaces_ws"`
	// RunnerWorkspaceID is the ID the Runner knows the workspace by (in this
	// codebase the workspace ID itself); empty until registration succeeds.
	RunnerWorkspaceID string `gorm:"size:64;not null;default:''"`
	Status            string `gorm:"size:16;not null;index"` // provisioning|ready|error|archived
	LastError         string `gorm:"not null;default:''"`
	Attempts          int    `gorm:"not null;default:0"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (ModelWorkspaceRecord) TableName() string { return "model_workspaces" }

// ApprovalNonceRecord records each one-time approval nonce the Runner has
// consumed, so a signed execution request cannot be replayed with a new
// request_id. The unique primary key is the fail-closed replay guard.
type ApprovalNonceRecord struct {
	Nonce      string    `gorm:"primaryKey;size:128"`
	ConsumedAt time.Time `gorm:"not null"`
}
