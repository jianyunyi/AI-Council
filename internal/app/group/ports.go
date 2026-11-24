package group

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Actor is the caller as resolved by the transport layer.
type Actor struct {
	UserID         string
	CanCreateGroup bool // group:create
	ReadAny        bool // group:read:any
	ManageAny      bool // group:manage:any
}

var ErrDirectoryNotFound = errors.New("directory: not found")

type AIModelInfo struct {
	ID              string
	Handle          string
	DisplayName     string
	Model           string
	ProviderName    string
	Status          string // active|disabled|degraded
	WorkspaceStatus string // provisioning|ready|error|archived|""
}

// AIModelDirectory is a read-only view of the P4 AI model registry.
type AIModelDirectory interface {
	GetAIModel(ctx context.Context, id string) (AIModelInfo, error)
	FindAIModelByHandle(ctx context.Context, handle string) (AIModelInfo, error)
	SearchAIModels(ctx context.Context, prefix string, limit int) ([]AIModelInfo, error) // active only
}

type UserInfo struct {
	ID          string
	Username    string
	DisplayName string
}

type UserDirectory interface {
	GetUser(ctx context.Context, id string) (UserInfo, error)
	FindUserByUsername(ctx context.Context, username string) (UserInfo, error)
}

// WorkspaceProvisioner is WIRING W6. CreateGroupWorkspace must create a
// WorkspaceRecord with Kind=group_shared, register it in the workspace service's
// in-memory maps, and must NOT register a runner. ArchiveWorkspace sets Archived.
type WorkspaceProvisioner interface {
	CreateGroupWorkspace(ctx context.Context, name, ownerID string) (string, error)
	ArchiveWorkspace(ctx context.Context, workspaceID string) error
}

type AuditEntry struct {
	ActorID    string
	Action     string
	GroupID    string
	TargetType string
	TargetID   string
	Data       map[string]any
	At         time.Time
}

// AuditSink is WIRING W9.
type AuditSink interface {
	Record(ctx context.Context, e AuditEntry) error
}

// Error is a typed service error carrying an HTTP status and a stable code.
type Error struct {
	Status int
	Code   string
	Msg    string
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

func newErr(status int, code, msg string) *Error { return &Error{Status: status, Code: code, Msg: msg} }

var (
	errForbidden          = newErr(http.StatusForbidden, "forbidden", "not allowed")
	errNotFound           = newErr(http.StatusNotFound, "group_not_found", "group not found")
	errMemberNotFound     = newErr(http.StatusNotFound, "member_not_found", "member not found")
	errMessageNotFound    = newErr(http.StatusNotFound, "message_not_found", "message not found")
	errArchived           = newErr(http.StatusConflict, "group_archived", "group is archived")
	errVersionConflict    = newErr(http.StatusConflict, "version_conflict", "group was modified; reload and retry")
	errExpectedVersion    = newErr(http.StatusUnprocessableEntity, "expected_version_required", "expected_version is required")
	errMemberConflict     = newErr(http.StatusConflict, "member_exists", "member is already in the group")
	errLastOwner          = newErr(http.StatusConflict, "last_owner", "a group must keep at least one owner")
	errInvalidName        = newErr(http.StatusUnprocessableEntity, "invalid_name", "name must be 1..120 characters")
	errInvalidDesc        = newErr(http.StatusUnprocessableEntity, "invalid_description", "description must be at most 2000 characters")
	errInvalidMemberType  = newErr(http.StatusUnprocessableEntity, "invalid_member_type", "member_type must be user|ai")
	errInvalidRole        = newErr(http.StatusUnprocessableEntity, "invalid_group_role", "group_role must be owner|operator|viewer")
	errUserNotFound       = newErr(http.StatusUnprocessableEntity, "user_not_found", "user not found")
	errAIModelNotFound    = newErr(http.StatusUnprocessableEntity, "ai_model_not_found", "ai model not found")
	errAIModelNotActive   = newErr(http.StatusUnprocessableEntity, "ai_model_not_active", "ai model is not active")
	errAIRoleFixed        = newErr(http.StatusUnprocessableEntity, "ai_role_fixed", "AI members always have role ai")
	errHumanApproval      = newErr(http.StatusUnprocessableEntity, "human_approval_required", "require_human_approval is locked to true in v1")
	errInvalidBody        = newErr(http.StatusUnprocessableEntity, "invalid_body", "body_md must be 1..16000 characters")
	errInvalidClientMsgID = newErr(http.StatusUnprocessableEntity, "invalid_client_msg_id", "client_msg_id must be 1..64 characters")
	errIntentUnavailable  = newErr(http.StatusUnprocessableEntity, "intent_not_available", "dispatch and review intents are not available yet")
	errInvalidIntent      = newErr(http.StatusUnprocessableEntity, "invalid_intent", "intent must be chat|dispatch|review|result_review")
	errRequirementUnavail = newErr(http.StatusUnprocessableEntity, "requirement_not_available", "requirements are not available yet")
	errReplyNotFound      = newErr(http.StatusUnprocessableEntity, "reply_not_found", "reply_to_message_id not found in this group")
	errNotEditable        = newErr(http.StatusConflict, "message_not_editable", "only your own text messages can be edited, within 10 minutes")
	errViewerCannotPost   = newErr(http.StatusForbidden, "not_a_member", "only group members can post")
	errInvalidCursor      = newErr(http.StatusBadRequest, "invalid_cursor", "invalid cursor")
)

func invalidPolicy(msg string) *Error {
	return newErr(http.StatusUnprocessableEntity, "invalid_policy", msg)
}

func invalidParams(msg string) *Error {
	return newErr(http.StatusUnprocessableEntity, "params_out_of_range", msg)
}
