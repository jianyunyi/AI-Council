package group

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sync"
)

// fakeArtifacts is the in-memory ArtifactSource for P9 tests. Task
// artifacts are seeded with add(); copies land in saved under their
// copy ref. corruptRef makes the stored bytes stop matching the
// recorded digest — the copy-up path must catch the mismatch and
// delete the copy.
type fakeArtifacts struct {
	mu      sync.Mutex
	byTask  map[string][]ArtifactMeta
	blobs   map[string][]byte // source bytes by meta ref
	saved   map[string][]byte // copy-up bytes by copy ref
	corrupt map[string]bool
}

func newFakeArtifacts() *fakeArtifacts {
	return &fakeArtifacts{
		byTask: map[string][]ArtifactMeta{}, blobs: map[string][]byte{},
		saved: map[string][]byte{}, corrupt: map[string]bool{},
	}
}

func (f *fakeArtifacts) add(taskID string, m ArtifactMeta, content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m.TaskID = taskID
	if m.ID == "" {
		m.ID = m.Ref // the real bridge always sets ID = artifact record id
	}
	sum := sha256.Sum256(content)
	m.SHA256 = hex.EncodeToString(sum[:])
	m.SizeBytes = int64(len(content))
	f.blobs[m.Ref] = content
	f.byTask[taskID] = append(f.byTask[taskID], m)
}

func (f *fakeArtifacts) ListTaskArtifacts(_ context.Context, taskID string) ([]ArtifactMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ArtifactMeta(nil), f.byTask[taskID]...), nil
}

func (f *fakeArtifacts) Open(_ context.Context, ref string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.blobs[ref]
	if !ok {
		b, ok = f.saved[ref]
	}
	if !ok {
		return nil, errArtifactNotFound
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), b...))), nil
}

func (f *fakeArtifacts) Save(_ context.Context, workspaceID, name, mediaType string, r io.Reader) (string, string, int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", "", 0, err
	}
	sum := sha256.Sum256(b)
	ref := "copy_" + hex.EncodeToString(sum[:])[:16]
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved[ref] = b
	return ref, hex.EncodeToString(sum[:]), int64(len(b)), nil
}

func (f *fakeArtifacts) Delete(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.saved, ref)
	return nil
}

// corruptRef tampers with a seeded artifact's bytes so they no longer
// match the digest the fake still reports for it.
func (f *fakeArtifacts) corruptRef(ref string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b, ok := f.blobs[ref]; ok {
		f.blobs[ref] = append(append([]byte(nil), b...), 'X')
	}
	f.corrupt[ref] = true
}
