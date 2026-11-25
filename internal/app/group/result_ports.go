package group

import (
	"context"
	"io"
)

// Part B P9: result copy-up ports. When a push-down finishes, a
// kind="result" job copies the task's allowed artifacts into the
// group's shared workspace (digest-verified, manifest-listed), has the
// assignee model write the result report, and commits a result
// submission that a stage="result" review round judges.
//
// The ONLY window into the Part A artifact store is ArtifactSource,
// implemented in transport/council over the landed store (W8): records
// carry the SHA-256 recorded at write time, and the store verifies it
// again on load. A meta with no recorded digest is unverifiable and
// the copy-up fails closed (digest_missing) — bytes are never copied
// on trust.

// ArtifactMeta is a Part A artifact as seen by the copy-up (W8).
type ArtifactMeta struct {
	ID          string
	TaskID      string
	WorkspaceID string
	Name        string
	Kind        string
	MediaType   string
	SizeBytes   int64
	SHA256      string // recorded at write time; empty means unverifiable (fail closed)
	Ref         string
}

// ArtifactSource is implemented in transport/council over the Part A artifact store.
type ArtifactSource interface {
	ListTaskArtifacts(ctx context.Context, taskID string) ([]ArtifactMeta, error)
	Open(ctx context.Context, ref string) (io.ReadCloser, error)
	Save(ctx context.Context, workspaceID, name, mediaType string, r io.Reader) (ref, sha256 string, size int64, err error)
	Delete(ctx context.Context, ref string) error
}

// EnableResults turns on P9 (requires EnablePushdown). It is a setter
// like EnablePushdown so the landed constructor signature stays
// unchanged. Without it, rsEnqueueTx is a no-op and requirements keep
// the exact P8 behavior at executed/execution_failed.
func (s *Service) EnableResults(src ArtifactSource) { s.artifacts = src }

func (s *Service) resultsEnabled() bool { return s.pushdownEnabled() && s.artifacts != nil }
