package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aicouncil/aicouncil/internal/core/artifact"
	"gorm.io/gorm"
)

var ErrArtifactTampered = errors.New("artifact content hash mismatch")

type ArtifactRepository struct {
	db   *gorm.DB
	root string
}

func NewArtifactRepository(db *gorm.DB, root string) *ArtifactRepository {
	return &ArtifactRepository{db: db, root: root}
}

func (r *ArtifactRepository) Save(ctx context.Context, runID string, env artifact.Envelope) error {
	if env.ID == "" || env.Type == "" {
		return errors.New("artifact id and type are required")
	}
	if env.ContentHash == "" {
		sum := sha256.Sum256(env.Content)
		env.ContentHash = hex.EncodeToString(sum[:])
	}
	dir := filepath.Join(r.root, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	finalPath := filepath.Join(dir, env.ID+".json")
	tmp, err := os.CreateTemp(dir, ".artifact-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	raw, err := json.Marshal(env)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err = tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmpPath, finalPath); err != nil {
		return err
	}
	record := ArtifactRecord{ID: env.ID, RunID: runID, Type: env.Type, SchemaVersion: env.SchemaVersion, ParentID: env.ParentID, ContentHash: env.ContentHash, FilePath: finalPath}
	if err := r.db.WithContext(ctx).Save(&record).Error; err != nil {
		return fmt.Errorf("save artifact index: %w", err)
	}
	return nil
}

func (r *ArtifactRepository) Load(ctx context.Context, id string) (artifact.Envelope, error) {
	var record ArtifactRecord
	if err := r.db.WithContext(ctx).First(&record, "id = ?", id).Error; err != nil {
		return artifact.Envelope{}, err
	}
	raw, err := os.ReadFile(record.FilePath)
	if err != nil {
		return artifact.Envelope{}, err
	}
	var env artifact.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return artifact.Envelope{}, err
	}
	sum := sha256.Sum256(env.Content)
	if hex.EncodeToString(sum[:]) != record.ContentHash || env.ContentHash != record.ContentHash {
		return artifact.Envelope{}, ErrArtifactTampered
	}
	return env, nil
}
