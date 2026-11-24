// Package wskind defines workspace kinds and the execution guard.
// group_shared workspaces never have a Runner workspace and can never execute.
package wskind

import (
	"context"
	"errors"
	"fmt"
)

const (
	Standard     = "standard"
	ModelPrivate = "model_private"
	GroupShared  = "group_shared"
)

var (
	ErrNotExecutable = errors.New("workspace kind is not executable")
	ErrUnknownKind   = errors.New("unknown workspace kind")
)

// Normalize maps the legacy empty value to Standard.
func Normalize(k string) string {
	if k == "" {
		return Standard
	}
	return k
}

func Valid(k string) bool {
	switch Normalize(k) {
	case Standard, ModelPrivate, GroupShared:
		return true
	}
	return false
}

// CheckExecutable fails closed for group_shared and unknown kinds.
func CheckExecutable(kind string) error {
	switch Normalize(kind) {
	case Standard, ModelPrivate:
		return nil
	case GroupShared:
		return fmt.Errorf("%w: %s", ErrNotExecutable, GroupShared)
	default:
		return fmt.Errorf("%w: %q", ErrUnknownKind, kind)
	}
}

// KindLookup resolves a workspace's kind from storage.
type KindLookup interface {
	WorkspaceKind(ctx context.Context, workspaceID string) (string, error)
}

// GuardExecution must be called before any Runner registration or execute call.
func GuardExecution(ctx context.Context, lookup KindLookup, workspaceID string) error {
	kind, err := lookup.WorkspaceKind(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("resolve workspace kind: %w", err)
	}
	return CheckExecutable(kind)
}
