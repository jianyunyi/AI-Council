package sqlite

import (
	"os"
	"path/filepath"

	_ "github.com/glebarez/go-sqlite"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func Open(path string) (*gorm.DB, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}

	db, err := gorm.Open(sqlite.Dialector{DriverName: "sqlite", DSN: path}, &gorm.Config{})
	if err != nil {
		return nil, err
	}
	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		return nil, err
	}
	if path != ":memory:" {
		if err := db.Exec("PRAGMA journal_mode = WAL").Error; err != nil {
			return nil, err
		}
	}
	if err := db.AutoMigrate(&RunRecord{}, &AuditRecord{}, &ArtifactRecord{}, &ProviderProfileRecord{}, &WorkspaceRecord{}, &TaskRecord{}, &SeatRecord{}, &ModelInvocationRecord{}); err != nil {
		return nil, err
	}
	return db, nil
}
