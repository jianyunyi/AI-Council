package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func openTaskListDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "tasks.db"))
	require.NoError(t, err)
	return db
}

func seedTask(t *testing.T, db *gorm.DB, record TaskRecord) {
	t.Helper()
	require.NoError(t, db.Create(&record).Error)
}

func baseTask(id, workspaceID, owner string, updated time.Time) TaskRecord {
	return TaskRecord{
		ID: id, OwnerUserID: owner, WorkspaceID: workspaceID, CreatedBy: owner,
		Origin: "direct", Title: "Task " + id, Requirement: "Requirement for " + id,
		State: "DRAFT", PlanVersion: 1, CreatedAt: updated.Add(-time.Hour), UpdatedAt: updated,
	}
}

func TestListTasksFiltersAndOrdering(t *testing.T) {
	db := openTaskListDB(t)
	repo := NewTaskRepository(db)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	a := baseTask("task-1", "ws-1", "alice", t0)
	b := baseTask("task-2", "ws-1", "alice", t0.Add(time.Hour))
	b.State = "AWAITING_APPROVAL"
	c := baseTask("task-3", "ws-2", "bob", t0.Add(2*time.Hour))
	c.Origin = "group"
	for _, record := range []TaskRecord{a, b, c} {
		seedTask(t, db, record)
	}
	require.NoError(t, db.Create(&WorkspaceRecord{ID: "ws-1", OwnerUserID: "alice"}).Error)
	require.NoError(t, db.Create(&WorkspaceRecord{ID: "ws-2", OwnerUserID: "bob"}).Error)

	page, err := repo.ListTasks(ctx, TaskListQuery{})
	require.NoError(t, err)
	require.Len(t, page.Tasks, 3)
	require.Equal(t, "task-3", page.Tasks[0].ID, "newest updated first")
	require.Equal(t, "task-1", page.Tasks[2].ID)
	require.Empty(t, page.NextCursor)

	page, err = repo.ListTasks(ctx, TaskListQuery{States: []string{"AWAITING_APPROVAL"}})
	require.NoError(t, err)
	require.Len(t, page.Tasks, 1)
	require.Equal(t, "task-2", page.Tasks[0].ID)

	page, err = repo.ListTasks(ctx, TaskListQuery{WorkspaceIDs: []string{"ws-1", "ws-2"}, CreatedBy: "bob"})
	require.NoError(t, err)
	require.Len(t, page.Tasks, 1)
	require.Equal(t, "task-3", page.Tasks[0].ID)

	page, err = repo.ListTasks(ctx, TaskListQuery{Origins: []string{"group"}})
	require.NoError(t, err)
	require.Len(t, page.Tasks, 1)
	require.Equal(t, "task-3", page.Tasks[0].ID)

	page, err = repo.ListTasks(ctx, TaskListQuery{Query: "task-2"})
	require.NoError(t, err)
	require.Len(t, page.Tasks, 1)

	after := t0.Add(30 * time.Minute)
	page, err = repo.ListTasks(ctx, TaskListQuery{UpdatedAfter: &after})
	require.NoError(t, err)
	require.Len(t, page.Tasks, 2)

	before := t0.Add(30 * time.Minute)
	page, err = repo.ListTasks(ctx, TaskListQuery{UpdatedBefore: &before})
	require.NoError(t, err)
	require.Len(t, page.Tasks, 1)
	require.Equal(t, "task-1", page.Tasks[0].ID)
}

func TestListTasksKeysetPagination(t *testing.T) {
	db := openTaskListDB(t)
	repo := NewTaskRepository(db)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	// Two tasks share an updated_at to exercise the id tiebreaker.
	for _, record := range []TaskRecord{
		baseTask("task-1", "ws-1", "alice", t0),
		baseTask("task-2", "ws-1", "alice", t0),
		baseTask("task-3", "ws-1", "alice", t0.Add(-time.Hour)),
		baseTask("task-4", "ws-1", "alice", t0.Add(time.Hour)),
	} {
		seedTask(t, db, record)
	}
	var seen []string
	cursor := ""
	for pages := 0; pages < 5; pages++ {
		page, err := repo.ListTasks(ctx, TaskListQuery{Limit: 2, Cursor: cursor})
		require.NoError(t, err)
		for _, record := range page.Tasks {
			seen = append(seen, record.ID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	require.Equal(t, []string{"task-4", "task-2", "task-1", "task-3"}, seen)

	_, err := repo.ListTasks(ctx, TaskListQuery{Cursor: "not-a-cursor"})
	require.ErrorIs(t, err, ErrInvalidTaskCursor)
	_, err = repo.ListTasks(ctx, TaskListQuery{Cursor: "aGVsbG8"})
	require.ErrorIs(t, err, ErrInvalidTaskCursor)
}

func TestListTasksScoping(t *testing.T) {
	db := openTaskListDB(t)
	repo := NewTaskRepository(db)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	require.NoError(t, db.Create(&WorkspaceRecord{ID: "ws-own", OwnerUserID: "alice"}).Error)
	require.NoError(t, db.Create(&WorkspaceRecord{ID: "ws-shared", OwnerUserID: "bob"}).Error)
	require.NoError(t, db.Create(&WorkspaceRecord{ID: "ws-other", OwnerUserID: "carol"}).Error)
	require.NoError(t, db.Create(&WorkspaceShareRecord{WorkspaceID: "ws-shared", UserID: "alice", Access: "viewer", GrantedBy: "bob", CreatedAt: t0, UpdatedAt: t0}).Error)
	seedTask(t, db, baseTask("task-own", "ws-own", "alice", t0))
	seedTask(t, db, baseTask("task-shared", "ws-shared", "bob", t0.Add(time.Minute)))
	seedTask(t, db, baseTask("task-other", "ws-other", "carol", t0.Add(2*time.Minute)))

	page, err := repo.ListTasks(ctx, TaskListQuery{ScopeUserID: "alice"})
	require.NoError(t, err)
	require.Len(t, page.Tasks, 2, "own + shared workspaces only")

	// Filtering by an inaccessible workspace yields an empty list, not an error.
	page, err = repo.ListTasks(ctx, TaskListQuery{ScopeUserID: "alice", WorkspaceIDs: []string{"ws-other"}})
	require.NoError(t, err)
	require.Empty(t, page.Tasks)

	page, err = repo.ListTasks(ctx, TaskListQuery{ScopeUserID: "alice", WorkspaceIDs: []string{"ws-shared"}})
	require.NoError(t, err)
	require.Len(t, page.Tasks, 1)
	require.Equal(t, "task-shared", page.Tasks[0].ID)
}
