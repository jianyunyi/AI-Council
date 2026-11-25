package council

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/aicouncil/aicouncil/internal/app/group"
	artifactcore "github.com/aicouncil/aicouncil/internal/core/artifact"
	storage "github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// Part B P9: artifactBridge is the ONLY window from the group service
// into the Part A artifact store (W8). The landed store records the
// SHA-256 of every artifact's content at write time
// (ArtifactRecord.ContentHash, computed in SaveOwned / artifact.New)
// and re-verifies it on Load (ErrArtifactTampered), so the bridge
// never has to trust unverified bytes: ListTaskArtifacts surfaces the
// recorded digest, Open goes through the verifying Load, and a record
// without a digest would surface an empty SHA256 — which the group
// service fails closed as digest_missing. No Part A save-path change
// was needed.
//
// The bridge works on artifact ENVELOPE CONTENT (the JSON payload),
// which is what ContentHash covers and what the copy-up copies.
type artifactBridge struct{ api *API }

var _ group.ArtifactSource = artifactBridge{}

// artifactKindOf maps a Part A artifact type onto the group copy-up
// kind vocabulary. Only runner.verification is produced by real
// executions today; it is a report. Unknown types keep their name and
// are excluded by the service unless they match an allowed kind.
func artifactKindOf(recordType string) string {
	if recordType == "runner.verification" {
		return "report"
	}
	return recordType
}

// artifactNameOf derives a display name from the artifact type: the
// verification report gets a stable file name; anything else is the
// sanitized type + ".json" (envelope content is JSON).
func artifactNameOf(recordType string) string {
	if recordType == "runner.verification" {
		return "verification-report.json"
	}
	var b strings.Builder
	for _, r := range recordType {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = "artifact"
	}
	return name + ".json"
}

func (b artifactBridge) ListTaskArtifacts(ctx context.Context, taskID string) ([]group.ArtifactMeta, error) {
	a := b.api
	if a.db == nil || a.artifactRepo == nil {
		return nil, nil
	}
	var records []storage.ArtifactRecord
	if err := a.db.WithContext(ctx).Where("run_id = ?", taskID).Order("created_at ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	// The workspace comes from the in-memory task (the same source
	// the task views use); the group service cross-checks it against
	// the push-down's model workspace and skips anything else.
	workspaceID := ""
	a.mu.Lock()
	if t, ok := a.tasks[taskID]; ok {
		workspaceID = t.WorkspaceID
	}
	a.mu.Unlock()
	out := make([]group.ArtifactMeta, 0, len(records))
	for _, rec := range records {
		// Size comes from the verified envelope content (Load checks
		// the digest); a record that fails to load is skipped here —
		// it simply never enters the manifest.
		env, err := a.artifactRepo.Load(ctx, rec.ID)
		if err != nil {
			continue
		}
		out = append(out, group.ArtifactMeta{
			ID:          rec.ID,
			TaskID:      taskID,
			WorkspaceID: workspaceID,
			Name:        artifactNameOf(rec.Type),
			Kind:        artifactKindOf(rec.Type),
			MediaType:   "application/json",
			SizeBytes:   int64(len(env.Content)),
			SHA256:      rec.ContentHash,
			Ref:         rec.ID,
		})
	}
	return out, nil
}

func (b artifactBridge) Open(ctx context.Context, ref string) (io.ReadCloser, error) {
	env, err := b.api.artifactRepo.Load(ctx, ref)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(env.Content)), nil
}

// Save stores a copied artifact as a new envelope owned by nobody
// (run id ""), so group copies never collide with task runs. The
// returned digest is computed over the exact bytes read.
func (b artifactBridge) Save(ctx context.Context, workspaceID, name, mediaType string, r io.Reader) (string, string, int64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", "", 0, err
	}
	var idBytes [12]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return "", "", 0, err
	}
	copyID := "groupcopy_" + hex.EncodeToString(idBytes[:])
	env, err := artifactcore.New("group.copy", copyID, "", json.RawMessage(data))
	if err != nil {
		return "", "", 0, err
	}
	if err := b.api.artifactRepo.SaveOwned(ctx, "", "", env); err != nil {
		return "", "", 0, err
	}
	sum := sha256.Sum256(data)
	return copyID, hex.EncodeToString(sum[:]), int64(len(data)), nil
}

func (b artifactBridge) Delete(ctx context.Context, ref string) error {
	a := b.api
	if a.db == nil {
		return nil
	}
	var rec storage.ArtifactRecord
	if err := a.db.WithContext(ctx).Where("id = ?", ref).First(&rec).Error; err != nil {
		return nil // already gone
	}
	if rec.FilePath != "" {
		_ = os.Remove(rec.FilePath)
	}
	return a.db.WithContext(ctx).Delete(&rec).Error
}
