package sqlite

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/aicouncil/aicouncil/internal/core/audit"
	"github.com/aicouncil/aicouncil/internal/core/runstate"
	"gorm.io/gorm"
)

var (
	ErrIllegalTransition    = errors.New("illegal run state transition")
	ErrConcurrentTransition = errors.New("concurrent run state transition")
)

type Run struct {
	ID      string
	State   runstate.State
	Version uint64
}

type RunRepository struct {
	db *gorm.DB
}

func NewRunRepository(db *gorm.DB) *RunRepository {
	return &RunRepository{db: db}
}

func (r *RunRepository) Create(ctx context.Context, id string, state runstate.State) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		run := RunRecord{ID: id, State: string(state), Version: 1}
		if err := tx.Create(&run).Error; err != nil {
			return err
		}
		event := AuditRecord{
			ID:       newID(),
			RunID:    id,
			Sequence: 1,
			Type:     "run.created",
			Actor:    "system",
			Detail:   "run created",
		}
		return tx.Create(&event).Error
	})
}

func (r *RunRepository) Transition(ctx context.Context, id string, next runstate.State, actor, detail string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current RunRecord
		if err := tx.First(&current, "id = ?", id).Error; err != nil {
			return err
		}
		if !runstate.CanTransition(runstate.State(current.State), next) {
			return ErrIllegalTransition
		}

		nextVersion := current.Version + 1
		result := tx.Model(&RunRecord{}).
			Where("id = ? AND version = ?", id, current.Version).
			Updates(map[string]any{"state": string(next), "version": nextVersion})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrConcurrentTransition
		}

		event := AuditRecord{
			ID:       newID(),
			RunID:    id,
			Sequence: nextVersion,
			Type:     "state.transition",
			Actor:    actor,
			Detail:   detail,
		}
		return tx.Create(&event).Error
	})
}

func (r *RunRepository) Load(ctx context.Context, id string) (Run, []audit.Event, error) {
	var record RunRecord
	if err := r.db.WithContext(ctx).First(&record, "id = ?", id).Error; err != nil {
		return Run{}, nil, err
	}

	var records []AuditRecord
	if err := r.db.WithContext(ctx).
		Where("run_id = ?", id).
		Order("sequence ASC").
		Find(&records).Error; err != nil {
		return Run{}, nil, err
	}

	events := make([]audit.Event, 0, len(records))
	for _, item := range records {
		events = append(events, audit.Event{
			ID:        item.ID,
			RunID:     item.RunID,
			Sequence:  item.Sequence,
			Type:      item.Type,
			Actor:     item.Actor,
			Detail:    item.Detail,
			CreatedAt: item.CreatedAt,
		})
	}
	return Run{ID: record.ID, State: runstate.State(record.State), Version: record.Version}, events, nil
}

func newID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}
