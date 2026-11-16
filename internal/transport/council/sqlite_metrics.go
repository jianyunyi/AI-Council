package council

import (
	"context"
	"errors"
	"time"

	"github.com/aicouncil/aicouncil/internal/observability/metrics"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type sqliteMetricsLogger struct {
	inner   logger.Interface
	metrics *metrics.Metrics
}

const (
	sqliteBusyCode   = 5
	sqliteLockedCode = 6
)

func installSQLiteMetricsLogger(db *gorm.DB, collector *metrics.Metrics) {
	if existing, ok := db.Logger.(*sqliteMetricsLogger); ok {
		existing.metrics = collector
		return
	}
	db.Logger = &sqliteMetricsLogger{inner: db.Logger, metrics: collector}
}

func (l *sqliteMetricsLogger) LogMode(level logger.LogLevel) logger.Interface {
	return &sqliteMetricsLogger{inner: l.inner.LogMode(level), metrics: l.metrics}
}

func (l *sqliteMetricsLogger) Info(ctx context.Context, message string, args ...interface{}) {
	l.inner.Info(ctx, message, args...)
}

func (l *sqliteMetricsLogger) Warn(ctx context.Context, message string, args ...interface{}) {
	l.inner.Warn(ctx, message, args...)
}

func (l *sqliteMetricsLogger) Error(ctx context.Context, message string, args ...interface{}) {
	l.inner.Error(ctx, message, args...)
}

func (l *sqliteMetricsLogger) Trace(ctx context.Context, begin time.Time, query func() (string, int64), err error) {
	var coded interface{ Code() int }
	if err != nil && errors.As(err, &coded) {
		primaryCode := coded.Code() & 0xff
		if primaryCode == sqliteBusyCode || primaryCode == sqliteLockedCode {
			l.metrics.RecordSQLiteLockWait(time.Since(begin))
		}
	}
	l.inner.Trace(ctx, begin, query, err)
}
