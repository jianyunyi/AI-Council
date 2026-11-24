package sqlite

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// TaskListQuery describes one page of the task list. Scoping is expressed by
// the caller: when ScopeUserID is non-empty the query only returns tasks in
// workspaces the user owns or that are shared with them. All filters combine
// with AND; an inaccessible workspace filter simply yields an empty page.
type TaskListQuery struct {
	WorkspaceIDs  []string
	States        []string
	CreatedBy     string
	Origins       []string
	Query         string
	UpdatedAfter  *time.Time
	UpdatedBefore *time.Time
	// ScopeUserID restricts results to workspaces owned by or shared with
	// this user. Empty means unrestricted (callers gate that on a
	// task:read:any-style permission or a non-RBAC deployment).
	ScopeUserID string
	Cursor      string
	Limit       int
}

type TaskListPage struct {
	Tasks      []TaskRecord
	NextCursor string
}

type taskListCursor struct {
	UpdatedAt time.Time `json:"u"`
	ID        string    `json:"id"`
}

var ErrInvalidTaskCursor = errors.New("invalid task list cursor")

// DecodeTaskCursor validates and decodes a pagination cursor produced by
// EncodeTaskCursor.
func DecodeTaskCursor(value string) (time.Time, string, error) {
	cursor, err := decodeTaskCursor(value)
	if err != nil {
		return time.Time{}, "", err
	}
	return cursor.UpdatedAt, cursor.ID, nil
}

func EncodeTaskCursor(updatedAt time.Time, id string) string {
	raw, _ := json.Marshal(taskListCursor{UpdatedAt: updatedAt.UTC(), ID: id})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeTaskCursor(value string) (taskListCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return taskListCursor{}, ErrInvalidTaskCursor
	}
	var cursor taskListCursor
	if err := json.Unmarshal(raw, &cursor); err != nil || cursor.ID == "" || cursor.UpdatedAt.IsZero() {
		return taskListCursor{}, ErrInvalidTaskCursor
	}
	return cursor, nil
}

type TaskRepository struct{ db *gorm.DB }

func NewTaskRepository(db *gorm.DB) *TaskRepository { return &TaskRepository{db: db} }

// ListTasks returns one keyset page ordered by (updated_at DESC, id DESC).
func (r *TaskRepository) ListTasks(ctx context.Context, query TaskListQuery) (TaskListPage, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	db := r.db.WithContext(ctx).Model(&TaskRecord{})
	if query.ScopeUserID != "" {
		db = db.Where("workspace_id IN (SELECT id FROM workspace_records WHERE owner_user_id = ? UNION SELECT workspace_id FROM workspace_share_records WHERE user_id = ?)", query.ScopeUserID, query.ScopeUserID)
	}
	if len(query.WorkspaceIDs) > 0 {
		db = db.Where("workspace_id IN ?", query.WorkspaceIDs)
	}
	if len(query.States) > 0 {
		db = db.Where("state IN ?", query.States)
	}
	if query.CreatedBy != "" {
		db = db.Where("created_by = ?", query.CreatedBy)
	}
	if len(query.Origins) > 0 {
		db = db.Where("origin IN ?", query.Origins)
	}
	if strings.TrimSpace(query.Query) != "" {
		like := "%" + escapeLike(strings.TrimSpace(query.Query)) + "%"
		db = db.Where("(title LIKE ? ESCAPE '\\' OR requirement LIKE ? ESCAPE '\\' OR id LIKE ? ESCAPE '\\')", like, like, like)
	}
	if query.UpdatedAfter != nil {
		db = db.Where("updated_at > ?", *query.UpdatedAfter)
	}
	if query.UpdatedBefore != nil {
		db = db.Where("updated_at < ?", *query.UpdatedBefore)
	}
	if query.Cursor != "" {
		cursor, err := decodeTaskCursor(query.Cursor)
		if err != nil {
			return TaskListPage{}, err
		}
		db = db.Where("(updated_at < ? OR (updated_at = ? AND id < ?))", cursor.UpdatedAt, cursor.UpdatedAt, cursor.ID)
	}
	var rows []TaskRecord
	if err := db.Order("updated_at DESC").Order("id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return TaskListPage{}, err
	}
	page := TaskListPage{}
	if len(rows) > limit {
		last := rows[limit-1]
		page.NextCursor = EncodeTaskCursor(last.UpdatedAt, last.ID)
		rows = rows[:limit]
	}
	page.Tasks = rows
	return page, nil
}

func escapeLike(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
	return replacer.Replace(value)
}
