package sqlite

import "time"

type RunRecord struct {
	ID        string `gorm:"primaryKey;size:64"`
	State     string `gorm:"not null;size:32"`
	Version   uint64 `gorm:"not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

type AuditRecord struct {
	ID        string `gorm:"primaryKey;size:64"`
	RunID     string `gorm:"not null;uniqueIndex:idx_audit_run_sequence,priority:1;index"`
	Sequence  uint64 `gorm:"not null;uniqueIndex:idx_audit_run_sequence,priority:2"`
	Type      string `gorm:"not null;size:64"`
	Actor     string `gorm:"not null;size:128"`
	Detail    string `gorm:"not null"`
	CreatedAt time.Time
}
