package sqlite

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// ErrWorkspaceNotFound is returned by WorkspaceKindStore when the workspace
// does not exist. (workspaceaccess has its own error of the same name; this
// one belongs to the storage package.)
var ErrWorkspaceNotFound = errors.New("workspace not found")

// WorkspaceKindStore resolves workspace kinds from storage; it implements
// wskind.KindLookup.
type WorkspaceKindStore struct{ db *gorm.DB }

func NewWorkspaceKindStore(db *gorm.DB) *WorkspaceKindStore { return &WorkspaceKindStore{db: db} }

func (s *WorkspaceKindStore) WorkspaceKind(ctx context.Context, workspaceID string) (string, error) {
	var rec WorkspaceRecord
	err := s.db.WithContext(ctx).Select("kind").Where("id = ?", workspaceID).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrWorkspaceNotFound
	}
	return rec.Kind, err
}
