package council

import (
	"context"
	"database/sql"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aicouncil/aicouncil/internal/observability/metrics"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type testSQLiteCodeError int

func (e testSQLiteCodeError) Error() string { return "synthetic sqlite error" }
func (e testSQLiteCodeError) Code() int     { return int(e) }

func TestSQLiteMetricsLoggerRecordsOnlyBusyAndLockedWaits(t *testing.T) {
	collector := metrics.New()
	inner := logger.New(log.New(io.Discard, "", 0), logger.Config{LogLevel: logger.Silent})
	wrapped := &sqliteMetricsLogger{inner: inner, metrics: collector}
	query := func() (string, int64) { return "SELECT 1", 1 }
	wrapped.Trace(context.Background(), time.Now().Add(-25*time.Millisecond), query, testSQLiteCodeError(5))
	wrapped.Trace(context.Background(), time.Now().Add(-50*time.Millisecond), query, testSQLiteCodeError(6))
	wrapped.Trace(context.Background(), time.Now().Add(-time.Second), query, testSQLiteCodeError(1))

	recorder := httptest.NewRecorder()
	collector.Handler(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if got := recorder.Body.String(); !strings.Contains(got, "aicouncil_sqlite_lock_contentions_total 2") || !strings.Contains(got, "aicouncil_sqlite_lock_wait_seconds_count 2") {
		t.Fatalf("sqlite lock metrics missing or counted non-lock errors:\n%s", got)
	}
}

func TestSQLiteMetricsLoggerMeasuresRealBusyTimeoutWait(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "contention.sqlite") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(40)"
	pool, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(2)
	pool.SetMaxIdleConns(2)
	t.Cleanup(func() { _ = pool.Close() })
	collector := metrics.New()
	db, err := gorm.Open(sqlite.Dialector{DriverName: "sqlite", Conn: pool}, &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	installSQLiteMetricsLogger(db, collector)
	if err := db.AutoMigrate(&contentionMetricRow{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&contentionMetricRow{ID: 1}).Error; err != nil {
		t.Fatal(err)
	}
	transaction := db.Begin()
	if transaction.Error != nil {
		t.Fatal(transaction.Error)
	}
	if err := transaction.Model(&contentionMetricRow{}).Where("id = ?", 1).Update("value", 1).Error; err != nil {
		t.Fatal(err)
	}
	locked := db.Model(&contentionMetricRow{}).Where("id = ?", 1).Update("value", 2)
	if locked.Error == nil {
		t.Fatal("concurrent write unexpectedly bypassed the held SQLite write lock")
	}
	if err := transaction.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	if got := collector.SQLiteLockContentions.Load(); got != 1 {
		t.Fatalf("contention count=%d, want 1", got)
	}
	if got := collector.SQLiteLockWaitNanos.Load(); got < uint64(10*time.Millisecond) {
		t.Fatalf("observed lock wait=%s, want at least 10ms", time.Duration(got))
	}
}

type contentionMetricRow struct {
	ID    int `gorm:"primaryKey"`
	Value int
}
