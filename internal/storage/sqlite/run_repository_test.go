package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/aicouncil/aicouncil/internal/core/runstate"
)

func TestCreateAndTransitionAppendOrderedAuditEvents(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	if err := repo.Create(ctx, "run-1", runstate.Draft); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := repo.Transition(ctx, "run-1", runstate.Analyzing, "user", "task accepted"); err != nil {
		t.Fatalf("Transition() error = %v", err)
	}

	run, events, err := repo.Load(ctx, "run-1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if run.State != runstate.Analyzing {
		t.Fatalf("run state = %q, want %q", run.State, runstate.Analyzing)
	}
	if run.Version != 2 {
		t.Fatalf("run version = %d, want 2", run.Version)
	}
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2", len(events))
	}
	if events[0].Sequence != 1 || events[0].Type != "run.created" {
		t.Fatalf("first event = %#v, want run.created at sequence 1", events[0])
	}
	if events[1].Sequence != 2 || events[1].Type != "state.transition" {
		t.Fatalf("second event = %#v, want state.transition at sequence 2", events[1])
	}
	if events[1].Actor != "user" || events[1].Detail != "task accepted" {
		t.Fatalf("transition event = %#v, want actor and detail preserved", events[1])
	}
}

func TestIllegalTransitionRollsBackStateAndAudit(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	if err := repo.Create(ctx, "run-1", runstate.Draft); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	err := repo.Transition(ctx, "run-1", runstate.Executing, "system", "skip approval")
	if !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("Transition() error = %v, want ErrIllegalTransition", err)
	}

	run, events, err := repo.Load(ctx, "run-1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if run.State != runstate.Draft || run.Version != 1 {
		t.Fatalf("run after rollback = %#v, want DRAFT version 1", run)
	}
	if len(events) != 1 || events[0].Type != "run.created" {
		t.Fatalf("events after rollback = %#v, want only run.created", events)
	}
}

func newTestRepository(t *testing.T) *RunRepository {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "council.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return NewRunRepository(db)
}
