package task

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// TaskStore is the read side of task persistence. The SQLite task repository
// satisfies it; the transport API and this service share the same query so
// filters, scoping and cursor semantics cannot drift apart.
type TaskStore interface {
	ListTasks(context.Context, sqlite.TaskListQuery) (sqlite.TaskListPage, error)
}

// WithTaskStore makes List read from SQLite (the source of truth). Without a
// store, List falls back to the in-memory write-through cache.
func (s *Service) WithTaskStore(store TaskStore) *Service {
	s.store = store
	return s
}

type ListItem struct {
	ID          string
	Title       string
	State       string
	WorkspaceID string
	CreatedBy   string
	Origin      string
	PlanVersion int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ListResult struct {
	Items      []ListItem
	NextCursor string
}

// List returns one page of task summaries. When a TaskStore is configured the
// database answers; otherwise the in-memory cache is filtered with the same
// semantics (the cache only knows tasks created through this process, which
// is why production wiring always sets the store).
func (s *Service) List(ctx context.Context, query sqlite.TaskListQuery) (ListResult, error) {
	if s.store != nil {
		page, err := s.store.ListTasks(ctx, query)
		if err != nil {
			return ListResult{}, err
		}
		items := make([]ListItem, 0, len(page.Tasks))
		for _, record := range page.Tasks {
			createdBy := record.CreatedBy
			if createdBy == "" {
				createdBy = record.OwnerUserID
			}
			origin := record.Origin
			if origin == "" {
				origin = "direct"
			}
			items = append(items, ListItem{
				ID: record.ID, Title: record.Title, State: record.State,
				WorkspaceID: record.WorkspaceID, CreatedBy: createdBy, Origin: origin,
				PlanVersion: record.PlanVersion, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
			})
		}
		return ListResult{Items: items, NextCursor: page.NextCursor}, nil
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	s.mu.Lock()
	all := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		all = append(all, t)
	}
	s.mu.Unlock()
	contains := func(set []string, value string) bool {
		for _, item := range set {
			if item == value {
				return true
			}
		}
		return len(set) == 0
	}
	items := make([]ListItem, 0, len(all))
	for _, t := range all {
		if !contains(query.WorkspaceIDs, t.WorkspaceID) || !contains(query.States, string(t.State)) {
			continue
		}
		if query.Query != "" && !strings.Contains(strings.ToLower(t.Requirement+" "+t.ID), strings.ToLower(query.Query)) {
			continue
		}
		items = append(items, ListItem{ID: t.ID, State: string(t.State), WorkspaceID: t.WorkspaceID, Origin: "direct", PlanVersion: t.PlanVersion})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID > items[j].ID })
	if len(items) > limit {
		items = items[:limit]
	}
	return ListResult{Items: items}, nil
}
