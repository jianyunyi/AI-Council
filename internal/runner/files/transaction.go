package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/runner/pathguard"
)

var ErrBeforeHashMismatch = errors.New("before hash mismatch")

type Snapshot struct {
	Path    string
	Existed bool
	Mode    fs.FileMode
	Content []byte
	Hash    string
}
type Transaction struct {
	guard     *pathguard.Guard
	snapshots []Snapshot
}

func NewTransaction(guard *pathguard.Guard) *Transaction { return &Transaction{guard: guard} }
func (t *Transaction) Apply(ctx context.Context, patches []schema.Patch) ([]Snapshot, error) {
	t.snapshots = nil
	prepared := make([]struct {
		path string
		data []byte
		mode fs.FileMode
	}, 0, len(patches))
	for _, p := range patches {
		path, err := t.guard.ResolveWrite(p.Path)
		if err != nil {
			return nil, err
		}
		old, err := os.ReadFile(path)
		existed := err == nil
		mode := fs.FileMode(0o600)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if info, e := os.Stat(path); e == nil {
			mode = info.Mode()
		}
		if p.BeforeHash != "" && hashBytes(old) != p.BeforeHash {
			return nil, ErrBeforeHashMismatch
		}
		data, err := applyUnifiedDiff(old, p.UnifiedDiff)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, struct {
			path string
			data []byte
			mode fs.FileMode
		}{path, data, mode})
		t.snapshots = append(t.snapshots, Snapshot{Path: path, Existed: existed, Mode: mode, Content: append([]byte(nil), old...), Hash: hashBytes(old)})
	}
	for i, p := range prepared {
		if err := replaceFile(p.path, p.data, p.mode); err != nil {
			_ = t.restore()
			return nil, err
		}
		_ = i
	}
	return append([]Snapshot(nil), t.snapshots...), nil
}
func (t *Transaction) Restore() error { return t.restore() }
func (t *Transaction) restore() error {
	var first error
	for i := len(t.snapshots) - 1; i >= 0; i-- {
		s := t.snapshots[i]
		if !s.Existed {
			if err := os.Remove(s.Path); err != nil && !os.IsNotExist(err) && first == nil {
				first = err
			}
			continue
		}
		if err := replaceFile(s.Path, s.Content, s.Mode); err != nil && first == nil {
			first = err
		}
	}
	return first
}
func replaceFile(path string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".aicouncil-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func hashBytes(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }
func applyUnifiedDiff(original []byte, diff string) ([]byte, error) {
	lines := strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "---") {
		lines = lines[2:]
	}
	var out []string
	old := strings.Split(strings.TrimSuffix(string(original), "\n"), "\n")
	if len(old) == 1 && old[0] == "" {
		old = nil
	}
	oi := 0
	for i := 0; i < len(lines); {
		if lines[i] == "" {
			i++
			continue
		}
		if !strings.HasPrefix(lines[i], "@@") {
			i++
			continue
		}
		parts := strings.Fields(lines[i])
		if len(parts) < 3 {
			return nil, errors.New("invalid unified diff hunk")
		}
		startRaw := strings.TrimPrefix(parts[1], "-")
		startRaw = strings.Split(startRaw, ",")[0]
		start, _ := strconv.Atoi(startRaw)
		for oi < start-1 && oi < len(old) {
			out = append(out, old[oi])
			oi++
		}
		i++
		for i < len(lines) && !strings.HasPrefix(lines[i], "@@") {
			line := lines[i]
			if line == "\\ No newline at end of file" {
				i++
				continue
			}
			if line == "" {
				i++
				continue
			}
			switch line[0] {
			case ' ':
				if oi >= len(old) || old[oi] != line[1:] {
					return nil, errors.New("diff context mismatch")
				}
				out = append(out, old[oi])
				oi++
			case '-':
				if oi >= len(old) || old[oi] != line[1:] {
					return nil, errors.New("diff removal mismatch")
				}
				oi++
			case '+':
				out = append(out, line[1:])
			}
			i++
		}
	}
	for oi < len(old) {
		out = append(out, old[oi])
		oi++
	}
	return []byte(strings.Join(out, "\n") + "\n"), nil
}
