// Package context collects a bounded, safe-to-return workspace snapshot.
package context

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	defaultMaxFiles      = 200
	defaultMaxFileBytes  = 64 << 10
	defaultMaxTotalBytes = 2 << 20
)

// Limits bounds the amount of workspace content returned by a Collector.
type Limits struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

// File is a text file within a workspace snapshot.
type File struct {
	Path    string
	Content string
}

// Snapshot is the collected workspace content.
type Snapshot struct {
	Files []File
}

// Collector reads safe text files rooted at a supplied directory.
type Collector struct {
	limits Limits
}

// NewCollector creates a collector. Zero-valued limits use the defaults.
func NewCollector(limits Limits) *Collector {
	if limits.MaxFiles <= 0 {
		limits.MaxFiles = defaultMaxFiles
	}
	if limits.MaxFileBytes <= 0 {
		limits.MaxFileBytes = defaultMaxFileBytes
	}
	if limits.MaxTotalBytes <= 0 {
		limits.MaxTotalBytes = defaultMaxTotalBytes
	}
	return &Collector{limits: limits}
}

// Collect returns sorted relative safe text files beneath root.
func (c *Collector) Collect(ctx context.Context, root string) (Snapshot, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return Snapshot{}, err
	}
	var files []File
	var total int64
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if name == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() && skippedDir(name) {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if !entry.Type().IsRegular() || refusedName(name) || refusedRelPath(rel) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > c.limits.MaxFileBytes || len(files) >= c.limits.MaxFiles || total+info.Size() > c.limits.MaxTotalBytes {
			return nil
		}
		maxBytes := c.limits.MaxFileBytes
		if remaining := c.limits.MaxTotalBytes - total; remaining < maxBytes {
			maxBytes = remaining
		}
		content, err := readAtMost(path, maxBytes)
		if err != nil {
			if errors.Is(err, errUnsafePath) {
				return nil
			}
			return err
		}
		if content == nil || strings.IndexByte(string(content), 0) >= 0 || !utf8.Valid(content) {
			return nil
		}
		files = append(files, File{Path: filepath.ToSlash(rel), Content: string(content)})
		total += int64(len(content))
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return Snapshot{Files: files}, nil
}

func readAtMost(path string, maxBytes int64) ([]byte, error) {
	file, err := openNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maxBytes {
		return nil, nil
	}
	return content, nil
}

func refusedName(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, ".env") ||
		lower == ".netrc" || lower == "_netrc" || lower == ".npmrc" ||
		lower == "id_rsa" || lower == "id_dsa" || lower == "id_ecdsa" || lower == "id_ed25519" ||
		lower == ".git-credentials" || lower == ".pypirc" || lower == ".pgpass" ||
		lower == "kubeconfig" || lower == "credentials.json" || lower == ".dockercfg" {
		return true
	}
	switch filepath.Ext(lower) {
	case ".key", ".pem", ".p12", ".pfx", ".crt", ".cer", ".der", ".csr", ".jks", ".keystore", ".tfstate", ".kubeconfig":
		return true
	default:
		return false
	}
}

// skippedDir drops large or generated directories that would otherwise consume
// the file/byte budget without adding useful review context.
func skippedDir(name string) bool {
	switch strings.ToLower(name) {
	case "node_modules", "vendor", "dist", "build", "target", ".next", ".venv", "venv", "__pycache__", ".idea", ".vscode", ".terraform":
		return true
	default:
		return false
	}
}

// refusedRelPath rejects files living under sensitive credential directories
// (e.g. .aws/credentials, .ssh/id_rsa, .kube/config), aligning the snapshot
// filter with the Runner's write-side sensitivity rules.
func refusedRelPath(rel string) bool {
	for _, part := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		switch strings.ToLower(part) {
		case ".aws", ".ssh", ".kube", ".gnupg", ".docker":
			return true
		}
	}
	return false
}
