package sqlite

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// withImmediateTxLock makes every transaction on this DSN start with BEGIN IMMEDIATE.
// A deferred transaction that reads and then writes must upgrade its lock, and SQLite
// refuses that upgrade with SQLITE_BUSY immediately (the busy handler does not apply).
// BEGIN IMMEDIATE takes the write lock up front, where busy_timeout does apply.
func withImmediateTxLock(dsn string) string {
	if strings.Contains(dsn, "_txlock=") {
		return dsn // explicit setting wins (F3)
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "_txlock=immediate"
}

// verifyImmediateTxLock proves the driver honored _txlock (F1). Two connections each
// begin a transaction WITHOUT writing: under BEGIN IMMEDIATE the second one must wait,
// so it is given a short timeout and is expected to report busy.
func verifyImmediateTxLock(ctx context.Context, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	tx1, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("txlock check: begin 1: %w", err)
	}
	defer tx1.Rollback()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("txlock check: conn: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout = 50"); err != nil {
		return fmt.Errorf("txlock check: pragma: %w", err)
	}
	defer conn.ExecContext(context.Background(), "PRAGMA busy_timeout = 5000") //nolint:errcheck
	tx2, err := conn.BeginTx(ctx, nil)
	if err == nil {
		_ = tx2.Rollback()
		return fmt.Errorf("sqlite driver ignored _txlock=immediate: transactions are still deferred (WIRING F1)")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "locked") && !strings.Contains(strings.ToLower(err.Error()), "busy") {
		return fmt.Errorf("txlock check: unexpected error: %w", err)
	}
	return nil
}
