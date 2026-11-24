package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

	// Driver parameters are applied to every connection, including connections
	// created later by the pool. A single connection would deadlock callers that
	// read through the repository while holding a transaction.
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	dsn := path + separator + "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	pool, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	ready := false
	defer func() {
		if !ready {
			_ = pool.Close()
		}
	}()
	if path != ":memory:" {
		var mode string
		if err := pool.QueryRow("PRAGMA journal_mode = WAL").Scan(&mode); err != nil {
			return nil, fmt.Errorf("enable SQLite WAL: %w", err)
		}
		if mode != "wal" {
			return nil, fmt.Errorf("enable SQLite WAL: unexpected journal mode %q", mode)
		}
	}
	rows, err := pool.Query("PRAGMA integrity_check")
	if err != nil {
		return nil, fmt.Errorf("SQLite integrity check: %w", err)
	}
	var results []string
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("SQLite integrity check: %w", err)
		}
		results = append(results, result)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, fmt.Errorf("SQLite integrity check: %w", err)
	}
	if len(results) != 1 || results[0] != "ok" {
		return nil, fmt.Errorf("SQLite integrity check failed: %s", strings.Join(results, "; "))
	}
	db, err := gorm.Open(sqlite.Dialector{DriverName: "sqlite", Conn: pool}, &gorm.Config{})
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(&RunRecord{}, &AuditRecord{}, &ArtifactRecord{}, &ProviderProfileRecord{}, &WorkspaceRecord{}, &WorkspaceShareRecord{}, &RunnerWorkspaceRecord{}, &TaskRecord{}, &SeatRecord{}, &ModelInvocationRecord{}, &EventRecord{}, &ApprovalRecord{}, &ExecutionRecord{}, &UserRecord{}, &IdentityTokenRecord{}, &OAuthFlowRecord{}, &OAuthIdentityRecord{}, &RoleRecord{}, &UserRoleRecord{}, &PermissionRecord{}, &RolePermissionRecord{}, &AccessTokenRecord{}, &ApprovalNonceRecord{}, &TaskRejectionRecord{}, &CouncilRoleAssignmentRecord{}, &CouncilRoleAssignmentSetRecord{}, &AIModelRecord{}, &ModelWorkspaceRecord{}, &ChatGroupRecord{}, &ChatGroupMemberRecord{}, &ChatMessageRecord{}, &ChatMentionRecord{}, &ChatMessageEditRecord{}, &GroupEventRecord{}, &GroupRequirementRecord{}, &RequirementProposalRecord{}, &GroupJobRecord{}, &GroupModelInvocationRecord{}); err != nil {
		return nil, err
	}
	// Backfill the task-list columns added after the tasks table shipped:
	// rows created before title/created_by/origin existed carry empty values.
	if err := db.Exec("UPDATE task_records SET created_by = owner_user_id WHERE created_by IS NULL OR created_by = ''").Error; err != nil {
		return nil, fmt.Errorf("backfill task created_by: %w", err)
	}
	if err := db.Exec("UPDATE task_records SET origin = 'direct' WHERE origin IS NULL OR origin = ''").Error; err != nil {
		return nil, fmt.Errorf("backfill task origin: %w", err)
	}
	ready = true
	return db, nil
}
