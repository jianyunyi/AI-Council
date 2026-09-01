package sqlite

import (
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"time"
)

type Event struct {
	RunID     string
	Sequence  int64
	Type      string
	Data      json.RawMessage
	CreatedAt time.Time
}
type EventRecord struct {
	ID        uint   `gorm:"primaryKey"`
	RunID     string `gorm:"index:idx_event_run_seq,priority:1"`
	Sequence  int64  `gorm:"index:idx_event_run_seq,priority:2"`
	Type      string
	Data      []byte
	CreatedAt time.Time
}
type EventRepository struct{ db *gorm.DB }

func NewEventRepository(db *gorm.DB) *EventRepository { return &EventRepository{db: db} }
func (r *EventRepository) Append(ctx context.Context, runID, typ string, data any) (Event, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Event{}, err
	}
	var out Event
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var last EventRecord
		if err := tx.Where("run_id = ?", runID).Order("sequence DESC").First(&last).Error; err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		rec := EventRecord{RunID: runID, Sequence: last.Sequence + 1, Type: typ, Data: raw, CreatedAt: time.Now().UTC()}
		if err := tx.Create(&rec).Error; err != nil {
			return err
		}
		out = Event{RunID: rec.RunID, Sequence: rec.Sequence, Type: rec.Type, Data: json.RawMessage(rec.Data), CreatedAt: rec.CreatedAt}
		return nil
	})
	return out, err
}
func (r *EventRepository) After(ctx context.Context, runID string, after int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows []EventRecord
	if err := r.db.WithContext(ctx).Where("run_id = ? AND sequence > ?", runID, after).Order("sequence ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, x := range rows {
		out = append(out, Event{RunID: x.RunID, Sequence: x.Sequence, Type: x.Type, Data: json.RawMessage(x.Data), CreatedAt: x.CreatedAt})
	}
	return out, nil
}
