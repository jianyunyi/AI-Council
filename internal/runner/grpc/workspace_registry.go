package grpc

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"gorm.io/gorm"
)

var (
	ErrWorkspacePathOutsideRoot       = errors.New("workspace path outside configured root")
	ErrWorkspacePathAlreadyRegistered = errors.New("workspace path already registered")
	ErrWorkspaceIDRebound             = errors.New("workspace ID already registered to another path")
	ErrWorkspaceNotRegistered         = errors.New("workspace is not registered")
)

type WorkspaceRegistry struct {
	root string
	db   *gorm.DB
}

func NewWorkspaceRegistry(root string, db *gorm.DB) (*WorkspaceRegistry, error) {
	if db == nil {
		return nil, errors.New("workspace registry database is required")
	}
	realRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(realRoot); resolveErr == nil {
		realRoot = resolved
	}
	info, err := os.Stat(realRoot)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("workspace root must be a directory")
	}
	return &WorkspaceRegistry{root: filepath.Clean(realRoot), db: db}, nil
}

func (r *WorkspaceRegistry) Register(id, relativePath string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", ErrWorkspacePathOutsideRoot
	}
	var existing sqlite.RunnerWorkspaceRecord
	existingErr := r.db.Where("workspace_id = ?", id).First(&existing).Error
	if existingErr != nil && !errors.Is(existingErr, gorm.ErrRecordNotFound) {
		return "", existingErr
	}
	if strings.TrimSpace(relativePath) == "" || strings.HasPrefix(relativePath, "/") || filepath.IsAbs(relativePath) || filepath.VolumeName(relativePath) != "" || strings.Contains(relativePath, `\`) || strings.Contains(relativePath, ":") {
		if existingErr == nil {
			return "", ErrWorkspaceIDRebound
		}
		return "", ErrWorkspacePathOutsideRoot
	}
	candidate := filepath.Join(r.root, filepath.Clean(relativePath))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if existingErr == nil {
			return "", ErrWorkspaceIDRebound
		}
		return "", ErrWorkspacePathOutsideRoot
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil || !insideRoot(r.root, resolved) {
		if existingErr == nil {
			return "", ErrWorkspaceIDRebound
		}
		return "", ErrWorkspacePathOutsideRoot
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", ErrWorkspacePathOutsideRoot
	}

	if existingErr == nil {
		if existing.CanonicalRoot != resolved {
			return "", ErrWorkspaceIDRebound
		}
		return existing.CanonicalRoot, nil
	}
	var pathOwner sqlite.RunnerWorkspaceRecord
	err = r.db.Where("canonical_root = ?", resolved).First(&pathOwner).Error
	if err == nil {
		return "", ErrWorkspacePathAlreadyRegistered
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	row := sqlite.RunnerWorkspaceRecord{WorkspaceID: id, CanonicalRoot: resolved, CreatedAt: time.Now().UTC()}
	if err := r.db.Create(&row).Error; err != nil {
		// Unique constraints are the concurrency guard for ID and canonical root.
		var byID sqlite.RunnerWorkspaceRecord
		if e := r.db.Where("workspace_id = ?", id).First(&byID).Error; e == nil {
			if byID.CanonicalRoot != resolved {
				return "", ErrWorkspaceIDRebound
			}
			return byID.CanonicalRoot, nil
		}
		return "", ErrWorkspacePathAlreadyRegistered
	}
	return resolved, nil
}

func (r *WorkspaceRegistry) Resolve(id string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", ErrWorkspaceNotRegistered
	}
	var row sqlite.RunnerWorkspaceRecord
	err := r.db.Where("workspace_id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrWorkspaceNotRegistered
	}
	if err != nil {
		return "", err
	}
	current, err := filepath.EvalSymlinks(row.CanonicalRoot)
	if err != nil {
		return "", ErrWorkspacePathOutsideRoot
	}
	current, err = filepath.Abs(current)
	if err != nil || !insideRoot(r.root, current) || filepath.Clean(current) != filepath.Clean(row.CanonicalRoot) {
		return "", ErrWorkspacePathOutsideRoot
	}
	info, err := os.Stat(current)
	if err != nil || !info.IsDir() {
		return "", ErrWorkspacePathOutsideRoot
	}
	return current, nil
}

func insideRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
