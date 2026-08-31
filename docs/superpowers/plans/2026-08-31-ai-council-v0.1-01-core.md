# AI Council v0.1 Phase 1: Core Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create the Go monorepo, explicit run-state machine, versioned artifacts, transactional SQLite persistence, and healthy go-zero/Gin process shells.

**Architecture:** One Go module owns two commands and focused internal packages. Domain types have no framework dependencies; GORM adapters live behind repositories; transport packages depend inward on services.

**Tech Stack:** Go 1.25+, go-zero, Gin, GORM, SQLite, testify

---

## Locked file structure

```text
go.mod
go.sum
Makefile
cmd/council-server/main.go
cmd/workspace-runner/main.go
internal/core/runstate/state.go
internal/core/runstate/state_test.go
internal/core/artifact/artifact.go
internal/core/artifact/artifact_test.go
internal/core/audit/event.go
internal/storage/sqlite/db.go
internal/storage/sqlite/models.go
internal/storage/sqlite/run_repository.go
internal/storage/sqlite/run_repository_test.go
internal/transport/council/http.go
internal/transport/council/http_test.go
internal/transport/runner/http.go
internal/transport/runner/http_test.go
```

### Task 1: Bootstrap the Go module and verification commands

**Files:**
- Create: `go.mod`
- Create: `Makefile`
- Create: `.gitignore`

- [ ] **Step 1: Create the module**

Run:

```powershell
go mod init github.com/aicouncil/aicouncil
go mod edit -go=1.25.0
go get github.com/gin-gonic/gin@latest
go get github.com/zeromicro/go-zero@latest
go get gorm.io/gorm@latest
go get gorm.io/driver/sqlite@latest
go get github.com/stretchr/testify@latest
```

Expected: `go.mod` names `github.com/aicouncil/aicouncil` and declares Go 1.25.

- [ ] **Step 2: Add repository ignores and Make targets**

Create `.gitignore`:

```gitignore
.data/
.env
*.db
*.db-shm
*.db-wal
coverage.out
web/.next/
web/node_modules/
```

Create `Makefile`:

```makefile
.PHONY: test test-race vet

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...
```

- [ ] **Step 3: Verify the empty module**

Run: `go test ./...`

Expected: exit 0 with no packages or test files.

- [ ] **Step 4: Commit**

```powershell
git add go.mod go.sum Makefile .gitignore
git commit -m "build: bootstrap Go workspace"
```

### Task 2: Implement the explicit run-state machine

**Files:**
- Create: `internal/core/runstate/state_test.go`
- Create: `internal/core/runstate/state.go`

- [ ] **Step 1: Write the failing transition tests**

```go
package runstate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanTransition(t *testing.T) {
	require.True(t, CanTransition(Draft, Analyzing))
	require.True(t, CanTransition(Verifying, Replanning))
	require.True(t, CanTransition(Replanning, Reviewing))
	require.False(t, CanTransition(Draft, Executing))
	require.False(t, CanTransition(AwaitingApproval, Succeeded))
}

func TestTerminalStateCannotRestart(t *testing.T) {
	for _, state := range []State{Succeeded, Failed, Cancelled} {
		require.False(t, CanTransition(state, Analyzing), string(state))
	}
}
```

- [ ] **Step 2: Run the test and verify failure**

Run: `go test ./internal/core/runstate -run Test -v`

Expected: FAIL because `State` and `CanTransition` do not exist.

- [ ] **Step 3: Implement the minimal state machine**

```go
package runstate

type State string

const (
	Draft State = "DRAFT"
	Analyzing State = "ANALYZING"
	Proposing State = "PROPOSING"
	Reviewing State = "REVIEWING"
	Judging State = "JUDGING"
	RedTeam State = "RED_TEAM"
	AwaitingApproval State = "AWAITING_APPROVAL"
	Executing State = "EXECUTING"
	Verifying State = "VERIFYING"
	Replanning State = "REPLANNING"
	Succeeded State = "SUCCEEDED"
	Failed State = "FAILED"
	Cancelled State = "CANCELLED"
)

var transitions = map[State]map[State]struct{}{
	Draft: {Analyzing: {}, Cancelled: {}},
	Analyzing: {Proposing: {}, Failed: {}, Cancelled: {}},
	Proposing: {Reviewing: {}, Failed: {}, Cancelled: {}},
	Reviewing: {Judging: {}, Failed: {}, Cancelled: {}},
	Judging: {RedTeam: {}, Failed: {}, Cancelled: {}},
	RedTeam: {Judging: {}, AwaitingApproval: {}, Failed: {}, Cancelled: {}},
	AwaitingApproval: {Executing: {}, Cancelled: {}},
	Executing: {Verifying: {}, Failed: {}, Cancelled: {}},
	Verifying: {Succeeded: {}, Replanning: {}, Failed: {}, Cancelled: {}},
	Replanning: {Reviewing: {}, Failed: {}, Cancelled: {}},
}

func CanTransition(from, to State) bool {
	_, ok := transitions[from][to]
	return ok
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/core/runstate -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/core/runstate
git commit -m "feat: add explicit council run state machine"
```

### Task 3: Add versioned artifacts and canonical hashes

**Files:**
- Create: `internal/core/artifact/artifact_test.go`
- Create: `internal/core/artifact/artifact.go`

- [ ] **Step 1: Write the failing hash stability test**

```go
package artifact

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewProducesStableHash(t *testing.T) {
	a, err := New("proposal", "proposal-1", "", map[string]any{"risk": "low", "files": []string{"a.go"}})
	require.NoError(t, err)
	b, err := New("proposal", "proposal-1", "", map[string]any{"files": []string{"a.go"}, "risk": "low"})
	require.NoError(t, err)
	require.Equal(t, a.ContentHash, b.ContentHash)
	require.Equal(t, CurrentSchemaVersion, a.SchemaVersion)
}
```

- [ ] **Step 2: Verify the test fails**

Run: `go test ./internal/core/artifact -v`

Expected: FAIL because `New` is undefined.

- [ ] **Step 3: Implement the artifact envelope**

```go
package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const CurrentSchemaVersion = "1.0"

type Envelope struct {
	SchemaVersion string          `json:"schema_version"`
	Type          string          `json:"type"`
	ID            string          `json:"id"`
	ParentID      string          `json:"parent_id,omitempty"`
	Content       json.RawMessage `json:"content"`
	ContentHash   string          `json:"content_hash"`
}

func New(kind, id, parentID string, content any) (Envelope, error) {
	raw, err := json.Marshal(content)
	if err != nil {
		return Envelope{}, err
	}
	sum := sha256.Sum256(raw)
	return Envelope{SchemaVersion: CurrentSchemaVersion, Type: kind, ID: id, ParentID: parentID, Content: raw, ContentHash: hex.EncodeToString(sum[:])}, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/core/artifact -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/core/artifact
git commit -m "feat: add versioned council artifacts"
```

### Task 4: Persist state transitions and audit events atomically

**Files:**
- Create: `internal/core/audit/event.go`
- Create: `internal/storage/sqlite/models.go`
- Create: `internal/storage/sqlite/db.go`
- Create: `internal/storage/sqlite/run_repository.go`
- Create: `internal/storage/sqlite/run_repository_test.go`

- [ ] **Step 1: Write a failing transactional transition test**

```go
package sqlite

import (
	"context"
	"testing"

	"github.com/aicouncil/aicouncil/internal/core/runstate"
	"github.com/stretchr/testify/require"
)

func TestTransitionAppendsAuditEvent(t *testing.T) {
	db := openTestDB(t)
	repo := NewRunRepository(db)
	require.NoError(t, repo.Create(context.Background(), "run-1", runstate.Draft))
	require.NoError(t, repo.Transition(context.Background(), "run-1", runstate.Analyzing, "user", "task accepted"))
	run, events, err := repo.Load(context.Background(), "run-1")
	require.NoError(t, err)
	require.Equal(t, runstate.Analyzing, run.State)
	require.Len(t, events, 2)
}

func TestTransitionRejectsIllegalMove(t *testing.T) {
	db := openTestDB(t)
	repo := NewRunRepository(db)
	require.NoError(t, repo.Create(context.Background(), "run-1", runstate.Draft))
	require.ErrorIs(t, repo.Transition(context.Background(), "run-1", runstate.Executing, "system", "skip"), ErrIllegalTransition)
}
```

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/storage/sqlite -v`

Expected: FAIL because the repository does not exist.

- [ ] **Step 3: Implement the records and database opener**

Create `internal/core/audit/event.go` with `Event{ID, RunID, Sequence, Type, Actor, Detail, CreatedAt}`. Create GORM records `RunRecord` and `AuditRecord`, mapping `State` as a string. Implement `Open(path string)` with SQLite WAL mode and `AutoMigrate`.

Use this transaction boundary in `run_repository.go`:

```go
func (r *RunRepository) Transition(ctx context.Context, id string, next runstate.State, actor, detail string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current RunRecord
		if err := tx.First(&current, "id = ?", id).Error; err != nil { return err }
		if !runstate.CanTransition(runstate.State(current.State), next) { return ErrIllegalTransition }
		current.State = string(next)
		current.Version++
		if err := tx.Save(&current).Error; err != nil { return err }
		event := AuditRecord{ID: uuid.NewString(), RunID: id, Sequence: current.Version, Type: "state.transition", Actor: actor, Detail: detail}
		return tx.Create(&event).Error
	})
}
```

`Create` must write the run and sequence-one `run.created` event in one transaction. `Load` returns ordered events.

- [ ] **Step 4: Run repository tests**

Run: `go test ./internal/storage/sqlite -v`

Expected: PASS, including illegal-transition rollback.

- [ ] **Step 5: Commit**

```powershell
git add internal/core/audit internal/storage/sqlite go.mod go.sum
git commit -m "feat: persist runs and audit transitions atomically"
```

### Task 5: Start healthy go-zero and Gin process shells

**Files:**
- Create: `internal/transport/council/http_test.go`
- Create: `internal/transport/council/http.go`
- Create: `internal/transport/runner/http_test.go`
- Create: `internal/transport/runner/http.go`
- Create: `cmd/council-server/main.go`
- Create: `cmd/workspace-runner/main.go`

- [ ] **Step 1: Write failing health handler tests**

For each transport, call the handler with `httptest.NewRecorder()` and assert status 200 and body `{"status":"ok"}`. The Council test must exercise a go-zero `rest.Server`; the Runner test must exercise a Gin engine in test mode.

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/transport/... -v`

Expected: FAIL because the constructors are undefined.

- [ ] **Step 3: Implement transport constructors**

Council constructor:

```go
func NewServer(conf rest.RestConf) *rest.Server {
	server := rest.MustNewServer(conf)
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/healthz", Handler: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}})
	return server
}
```

Runner constructor:

```go
func NewRouter() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	return r
}
```

Each `main.go` reads its listen address from a flag, constructs the server, and handles SIGINT/SIGTERM with graceful shutdown.

- [ ] **Step 4: Run all Phase 1 checks**

Run:

```powershell
go test ./...
go vet ./...
```

Expected: both commands exit 0.

- [ ] **Step 5: Commit**

```powershell
git add cmd internal/transport go.mod go.sum
git commit -m "feat: add council and runner process shells"
```

## Phase 1 completion gate

Run `go test -race ./...` and `go vet ./...`. Both must exit 0. Start both commands locally and confirm `/healthz` returns 200 before beginning Phase 2.
