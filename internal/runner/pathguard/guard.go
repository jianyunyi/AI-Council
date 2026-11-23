package pathguard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrOutsideWorkspace = errors.New("path outside workspace")
	ErrSensitivePath    = errors.New("sensitive path")
	ErrFileTooLarge     = errors.New("file too large")
	ErrGitInternalPath  = errors.New("refusing to write inside .git")
)

type Guard struct {
	root     string
	maxBytes int64
}

func New(root string, maxBytes int64) (*Guard, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		real = abs
	}
	return &Guard{root: filepath.Clean(real), maxBytes: maxBytes}, nil
}

func (g *Guard) ResolveRead(relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", ErrOutsideWorkspace
	}
	candidate := filepath.Join(g.root, filepath.Clean(relative))
	if err := g.checkInside(candidate); err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		real = candidate
	}
	if err := g.checkInside(real); err != nil {
		return "", err
	}
	if isSensitive(real) {
		return "", ErrSensitivePath
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if g.maxBytes > 0 && info.Size() > g.maxBytes {
		return "", ErrFileTooLarge
	}
	return real, nil
}

func (g *Guard) ResolveWrite(relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", ErrOutsideWorkspace
	}
	candidate := filepath.Join(g.root, filepath.Clean(relative))
	if hasGitComponent(g.root, candidate) {
		return "", ErrGitInternalPath
	}
	if err := g.checkInside(candidate); err != nil {
		return "", err
	}
	if isSensitive(candidate) {
		return "", ErrSensitivePath
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(candidate))
	if err != nil {
		parent = filepath.Dir(candidate)
	}
	if err := g.checkInside(parent); err != nil {
		return "", err
	}
	return candidate, nil
}

func (g *Guard) ResolveDirectory(relative string) (string, error) { return g.ResolveRead(relative) }
func (g *Guard) Root() string                                     { return g.root }

func (g *Guard) checkInside(path string) error {
	rel, err := filepath.Rel(g.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ErrOutsideWorkspace
	}
	return nil
}

func isSensitive(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if base == ".env" || strings.HasPrefix(base, ".env.") || base == "id_rsa" || base == "id_ed25519" || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") {
		return true
	}
	return strings.HasSuffix(strings.ToLower(path), ".db") || strings.HasSuffix(strings.ToLower(path), ".sqlite")
}

// hasGitComponent reports whether the candidate path lies within a .git
// directory relative to the workspace root. Writing there (e.g. .git/config
// core.fsmonitor or hooks) would let an approved patch run arbitrary code the
// next time git is invoked, bypassing the argv-only command boundary.
func hasGitComponent(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	for _, part := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if strings.EqualFold(part, ".git") {
			return true
		}
	}
	return false
}
