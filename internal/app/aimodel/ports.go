package aimodel

import (
	"context"
	"errors"
	"net/http"
)

// ProfileInfo is the masked view of a provider profile. It never carries keys.
type ProfileInfo struct {
	ID           string
	Name         string
	ProviderType string // "openai" | "anthropic" | "deepseek"
	Enabled      bool
	HasKey       bool
}

var ErrProfileNotFound = errors.New("provider profile not found")

// ProfileLookup resolves a provider profile by ID. In this codebase profiles
// are stored per owner, but AI models are an admin-enabled registry keyed by
// profile ID (the enabling actor is recorded as CreatedBy), so the lookup is
// by ID alone.
type ProfileLookup interface {
	GetProfile(ctx context.Context, id string) (ProfileInfo, error)
}

// ModelLister returns the models a profile can serve. May be nil, in which
// case created models carry a model_unverified warning.
type ModelLister interface {
	ListModels(ctx context.Context, profileID string) ([]string, error)
}

// WorkspaceProvisioner creates and archives workspaces on behalf of the
// service. CreateModelWorkspace must create a workspace with
// Kind=wskind.ModelPrivate and return its ID.
type WorkspaceProvisioner interface {
	CreateModelWorkspace(ctx context.Context, name, ownerID string) (string, error)
	ArchiveWorkspace(ctx context.Context, workspaceID string) error
	UnarchiveWorkspace(ctx context.Context, workspaceID string) error
}

// RunnerRegistrar registers a workspace with the Runner. It MUST be
// idempotent per workspaceID and MUST call wskind.GuardExecution before
// registering.
type RunnerRegistrar interface {
	EnsureRunnerWorkspace(ctx context.Context, workspaceID string) (runnerWorkspaceID string, err error)
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
	errInvalidHandle  = newErr(http.StatusUnprocessableEntity, "invalid_handle", "handle must match ^[a-z0-9][a-z0-9-]{1,31}$")
	errInvalidName    = newErr(http.StatusUnprocessableEntity, "invalid_display_name", "display_name must be 1..120 chars")
	errInvalidModel   = newErr(http.StatusUnprocessableEntity, "invalid_model", "model must be 1..200 chars")
	errProfileMissing = newErr(http.StatusUnprocessableEntity, "profile_not_found", "provider profile not found")
	errProfileOff     = newErr(http.StatusUnprocessableEntity, "profile_disabled", "provider profile is disabled")
	errProfileNoKey   = newErr(http.StatusUnprocessableEntity, "profile_key_missing", "provider profile has no API key")
	errModelNotOffer  = newErr(http.StatusUnprocessableEntity, "model_not_offered", "model is not offered by this provider profile")
	errConflict       = newErr(http.StatusConflict, "conflict", "handle or (profile, model) already enabled")
	errNotFound       = newErr(http.StatusNotFound, "not_found", "ai model not found")
	errInvalidStatus  = newErr(http.StatusUnprocessableEntity, "invalid_status", "status must be active|disabled|degraded")
	errNotRetryable   = newErr(http.StatusConflict, "not_retryable", "workspace is not in error state")
)
