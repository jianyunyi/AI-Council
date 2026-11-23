package backup

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	_ "github.com/glebarez/go-sqlite"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type entry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type manifest struct {
	Version int     `json:"version"`
	Entries []entry `json:"entries"`
}

// Create backs up the council database and artifacts. Retained for compatibility.
func Create(dbPath, artifacts, output string, key []byte) error {
	return CreateWithRunner(dbPath, "", artifacts, output, key)
}

// CreateWithRunner additionally snapshots the Runner database when runnerDBPath
// is non-empty, so a restore recovers idempotency/workspace-registry state too.
func CreateWithRunner(dbPath, runnerDBPath, artifacts, output string, key []byte) error {
	if len(key) != 32 {
		return errors.New("backup key must be 32 bytes")
	}
	if dbPath == "" || artifacts == "" || output == "" {
		return errors.New("db, artifacts and output are required")
	}
	if _, err := os.Stat(dbPath); err != nil {
		return err
	}
	if st, err := os.Stat(artifacts); err != nil || !st.IsDir() {
		return errors.New("artifact directory is required")
	}
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(output), ".aicouncil-snapshot-*.db")
	if err != nil {
		return err
	}
	snapshot := tmp.Name()
	tmp.Close()
	os.Remove(snapshot)
	defer os.Remove(snapshot)
	quoted := strings.ReplaceAll(snapshot, "'", "''")
	src, err := os.OpenFile(dbPath, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	src.Close()
	db, err := openSQLite(dbPath)
	if err != nil {
		return err
	}
	_, err = db.Exec("VACUUM INTO '" + quoted + "'")
	db.Close()
	if err != nil {
		return fmt.Errorf("sqlite snapshot: %w", err)
	}
	if err = os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		return err
	}
	plain, err := os.CreateTemp(filepath.Dir(output), ".aicouncil-archive-*.zip")
	if err != nil {
		return err
	}
	name := plain.Name()
	defer os.Remove(name)
	zw := zip.NewWriter(plain)
	entries := []entry{}
	add := func(path, source string) error {
		f, e := os.Open(source)
		if e != nil {
			return e
		}
		defer f.Close()
		h := sha256.New()
		w, e := zw.Create(path)
		if e != nil {
			return e
		}
		n, e := io.Copy(io.MultiWriter(w, h), f)
		if e != nil {
			return e
		}
		entries = append(entries, entry{path, hex.EncodeToString(h.Sum(nil)), n})
		return nil
	}
	if err = add("council.db", snapshot); err == nil {
		if runnerDBPath != "" {
			runnerSnapshot := snapshot + ".runner"
			defer os.Remove(runnerSnapshot)
			rdb, rerr := openSQLite(runnerDBPath)
			if rerr != nil {
				return rerr
			}
			_, rerr = rdb.Exec("VACUUM INTO '" + strings.ReplaceAll(runnerSnapshot, "'", "''") + "'")
			rdb.Close()
			if rerr != nil {
				return fmt.Errorf("sqlite runner snapshot: %w", rerr)
			}
			if err = add("runner.db", runnerSnapshot); err != nil {
				return err
			}
		}
		err = filepath.WalkDir(artifacts, func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				return fmt.Errorf("unsupported artifact file: %s", p)
			}
			rel, e := filepath.Rel(artifacts, p)
			if e != nil {
				return e
			}
			return add("artifacts/"+filepath.ToSlash(rel), p)
		})
	}
	if err == nil {
		b, _ := json.Marshal(manifest{1, entries})
		w, e := zw.Create("manifest.json")
		if e != nil {
			err = e
		} else {
			_, err = w.Write(b)
		}
	}
	if e := zw.Close(); err == nil {
		err = e
	}
	if e := plain.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	block, _ := aes.NewCipher(key)
	g, _ := cipher.NewGCM(block)
	nonce := make([]byte, g.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	encrypted := append([]byte("AICB1"), nonce...)
	encrypted = g.Seal(encrypted, nonce, data, nil)
	out, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = out.Write(encrypted)
	return err
}

func openSQLite(path string) (*sql.DB, error) { return sql.Open("sqlite", path) }

func Restore(archive, destination string, key []byte) error {
	if len(key) != 32 {
		return errors.New("backup key must be 32 bytes")
	}
	if archive == "" || destination == "" {
		return errors.New("archive and destination are required")
	}
	if _, e := os.Stat(destination); !errors.Is(e, os.ErrNotExist) {
		return errors.New("restore destination must not exist")
	}
	raw, e := os.ReadFile(archive)
	if e != nil {
		return e
	}
	if len(raw) < 5 || string(raw[:5]) != "AICB1" {
		return errors.New("invalid backup format")
	}
	b, e := aes.NewCipher(key)
	if e != nil {
		return e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return e
	}
	if len(raw) < 5+g.NonceSize() {
		return errors.New("truncated backup")
	}
	plain, e := g.Open(nil, raw[5:5+g.NonceSize()], raw[5+g.NonceSize():], nil)
	if e != nil {
		return errors.New("backup authentication failed")
	}
	zr, e := zip.NewReader(bytes.NewReader(plain), int64(len(plain)))
	if e != nil {
		return e
	}
	files := map[string][]byte{}
	var manifestData []byte
	for _, f := range zr.File {
		if f.Name == "manifest.json" {
			r, e := f.Open()
			if e != nil {
				return e
			}
			manifestData, e = io.ReadAll(r)
			r.Close()
			if e != nil {
				return e
			}
			continue
		}
		clean := filepath.Clean(filepath.FromSlash(f.Name))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
			return errors.New("unsafe archive path")
		}
		r, e := f.Open()
		if e != nil {
			return e
		}
		data, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			return e
		}
		files[filepath.ToSlash(clean)] = data
	}
	var m manifest
	if manifestData == nil {
		return errors.New("backup manifest missing")
	}
	if e = json.Unmarshal(manifestData, &m); e != nil || m.Version != 1 {
		return errors.New("invalid backup manifest")
	}
	if len(files) != len(m.Entries) {
		return errors.New("backup manifest entry count mismatch")
	}
	for _, ent := range m.Entries {
		data, ok := files[ent.Path]
		if !ok {
			return fmt.Errorf("backup entry missing: %s", ent.Path)
		}
		sum := sha256.Sum256(data)
		if int64(len(data)) != ent.Size || hex.EncodeToString(sum[:]) != ent.SHA256 {
			return fmt.Errorf("backup checksum mismatch: %s", ent.Path)
		}
	}
	if _, ok := files["council.db"]; !ok {
		return errors.New("database missing")
	}
	if e = os.Mkdir(destination, 0700); e != nil {
		return e
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(destination)
		}
	}()
	for name, data := range files {
		if name == "manifest.json" {
			continue
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			return e
		}
		if e = os.WriteFile(target, data, 0600); e != nil {
			return e
		}
	}
	db, e := openSQLite(filepath.Join(destination, "council.db"))
	if e != nil {
		return e
	}
	var integrity string
	e = db.QueryRow("PRAGMA integrity_check").Scan(&integrity)
	db.Close()
	if e != nil || integrity != "ok" {
		return errors.New("restored database integrity check failed")
	}
	// runner.db is optional for backward compatibility with older archives.
	if _, ok := files["runner.db"]; ok {
		rdb, re := openSQLite(filepath.Join(destination, "runner.db"))
		if re != nil {
			return re
		}
		var runnerIntegrity string
		re = rdb.QueryRow("PRAGMA integrity_check").Scan(&runnerIntegrity)
		rdb.Close()
		if re != nil || runnerIntegrity != "ok" {
			return errors.New("restored runner database integrity check failed")
		}
	}
	success = true
	return nil
}
