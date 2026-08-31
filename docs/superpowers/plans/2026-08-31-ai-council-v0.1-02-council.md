# AI Council v0.1 Phase 2: Providers and Council Protocol Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Connect three model providers and produce independent proposals, anonymous peer reviews, a Judge decision, a red-team report, and a bounded execution plan without granting any execution capability.

**Architecture:** Provider adapters normalize all vendor APIs into one interface. The Council engine accepts interfaces for models, storage, clocks, and IDs, making the workflow deterministic under tests. Every stage emits a versioned artifact before the state advances.

**Tech Stack:** Go, official OpenAI and Anthropic Go SDKs, DeepSeek HTTP API, GORM repositories, httptest, testify

---

## Locked file structure

```text
internal/provider/provider.go
internal/provider/registry.go
internal/provider/contract_test.go
internal/provider/openai/provider.go
internal/provider/anthropic/provider.go
internal/provider/deepseek/provider.go
internal/provider/secrets/memory.go
internal/provider/secrets/memory_test.go
internal/council/schema/types.go
internal/council/engine.go
internal/council/engine_test.go
internal/council/anonymize.go
internal/council/anonymize_test.go
internal/council/limits.go
internal/council/limits_test.go
internal/storage/sqlite/artifact_repository.go
internal/storage/sqlite/council_repository.go
internal/storage/sqlite/council_repository_test.go
```

### Task 1: Define the provider contract and in-memory secret vault

**Files:**
- Create: `internal/provider/provider.go`
- Create: `internal/provider/registry.go`
- Create: `internal/provider/secrets/memory_test.go`
- Create: `internal/provider/secrets/memory.go`

- [ ] **Step 1: Write the failing vault test**

```go
package secrets

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMemoryVaultNeverListsSecretValues(t *testing.T) {
	vault := NewMemoryVault()
	vault.Put("profile-1", "sk-test-secret")
	require.Equal(t, "sk-test-secret", mustGet(t, vault, "profile-1"))
	require.Equal(t, []string{"profile-1"}, vault.IDs())
	vault.Delete("profile-1")
	_, ok := vault.Get("profile-1")
	require.False(t, ok)
}
```

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/provider/secrets -v`

Expected: FAIL because `MemoryVault` is undefined.

- [ ] **Step 3: Implement the contracts**

```go
package provider

import "context"

type Message struct { Role, Content string }
type Usage struct { InputTokens, OutputTokens int64; EstimatedCostMicros int64 }
type Request struct { Model string; Messages []Message; JSONSchema []byte; Temperature float64 }
type Response struct { Content []byte; Usage Usage; ProviderRequestID string }

type ModelProvider interface {
	Name() string
	Generate(context.Context, Request) (Response, error)
}

type Registry struct { providers map[string]ModelProvider }
func NewRegistry(items ...ModelProvider) *Registry {
	r := &Registry{providers: map[string]ModelProvider{}}
	for _, item := range items { r.providers[item.Name()] = item }
	return r
}
func (r *Registry) Get(name string) (ModelProvider, bool) { p, ok := r.providers[name]; return p, ok }
```

Implement `MemoryVault` with a `sync.RWMutex`, a private `map[string][]byte`, defensive copies on `Put` and `Get`, sorted IDs, and zeroing bytes on `Delete`. Do not add JSON tags or persistence methods.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/provider/... -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/provider
git commit -m "feat: define model provider and ephemeral secret vault"
```

### Task 2: Implement OpenAI, Anthropic, and DeepSeek adapters

**Files:**
- Create: `internal/provider/contract_test.go`
- Create: `internal/provider/openai/provider.go`
- Create: `internal/provider/anthropic/provider.go`
- Create: `internal/provider/deepseek/provider.go`

- [ ] **Step 1: Add dependencies and failing provider contract tests**

Run:

```powershell
go get github.com/openai/openai-go/v3@latest
go get github.com/anthropics/anthropic-sdk-go@latest
```

Create a table-driven test that starts `httptest.Server` instances returning one valid vendor response and one 429 response. Construct each adapter with an injected base URL and HTTP client. Assert that all adapters return normalized `Response.Content`, token usage, request ID, and an error satisfying `errors.Is(err, provider.ErrRateLimited)` for 429.

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/provider/... -run Contract -v`

Expected: FAIL because the adapters and normalized errors do not exist.

- [ ] **Step 3: Implement normalized errors and adapters**

Add to `provider.go`:

```go
var (
	ErrUnauthorized = errors.New("provider unauthorized")
	ErrRateLimited = errors.New("provider rate limited")
	ErrInvalidOutput = errors.New("provider invalid output")
)

type APIError struct { Provider string; Status int; Kind error; Message string }
func (e *APIError) Error() string { return fmt.Sprintf("%s: status %d: %s", e.Provider, e.Status, e.Message) }
func (e *APIError) Unwrap() error { return e.Kind }
```

OpenAI uses `openai-go/v3` Responses API and an injected `option.WithBaseURL`. Anthropic uses `anthropic-sdk-go` Messages API and its base URL option. DeepSeek posts an OpenAI-compatible chat-completions request using `net/http`. Each adapter must:

```go
func (p *Provider) Name() string
func (p *Provider) Generate(ctx context.Context, req provider.Request) (provider.Response, error)
```

Map only vendor response fields into the normalized response. Never include API keys in returned errors; truncate provider bodies to 2 KiB after applying the shared redactor.

- [ ] **Step 4: Run adapter tests**

Run: `go test ./internal/provider/... -v`

Expected: PASS without real network calls or real API keys.

- [ ] **Step 5: Commit**

```powershell
git add internal/provider go.mod go.sum
git commit -m "feat: add three model provider adapters"
```

### Task 3: Define Council schemas and persist artifacts

**Files:**
- Create: `internal/council/schema/types.go`
- Create: `internal/storage/sqlite/artifact_repository.go`
- Create: `internal/storage/sqlite/artifact_repository_test.go`
- Create: `internal/storage/sqlite/council_repository.go`
- Create: `internal/storage/sqlite/council_repository_test.go`

- [ ] **Step 1: Write a failing round-trip test**

Create a `Proposal` with requirements, file changes, risks, tests, and evidence; wrap it in `artifact.Envelope`; save and load it through `ArtifactRepository`; assert the content hash and raw JSON are unchanged. Save a provider profile, workspace, task, three seats, and a model invocation; assert usage and model metadata round-trip while the raw SQLite bytes do not contain the test API key.

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/storage/sqlite -run Artifact -v`

Expected: FAIL because Council schemas and the repository do not exist.

- [ ] **Step 3: Implement the version-one schemas**

```go
package schema

type TaskBrief struct { Requirement string `json:"requirement"`; Constraints []string `json:"constraints"`; Acceptance []string `json:"acceptance"`; WorkspaceFacts []Evidence `json:"workspace_facts"` }
type Evidence struct { Path string `json:"path"`; Line int `json:"line,omitempty"`; Claim string `json:"claim"` }
type FileChange struct { Path string `json:"path"`; Operation string `json:"operation"`; Rationale string `json:"rationale"` }
type Proposal struct { ID string `json:"id"`; Summary string `json:"summary"`; Approach []string `json:"approach"`; Files []FileChange `json:"files"`; Risks []string `json:"risks"`; Tests []string `json:"tests"`; Evidence []Evidence `json:"evidence"` }
type PeerReview struct { ReviewerSeatID string `json:"reviewer_seat_id"`; ProposalAlias string `json:"proposal_alias"`; Correctness []string `json:"correctness"`; Risks []string `json:"risks"`; Missing []string `json:"missing"`; Verdict string `json:"verdict"` }
type CouncilDecision struct { SelectedAliases []string `json:"selected_aliases"`; Reasons []string `json:"reasons"`; Rejected map[string][]string `json:"rejected"`; PlanSummary []string `json:"plan_summary"` }
type RedTeamReport struct { Blocking []string `json:"blocking"`; NonBlocking []string `json:"non_blocking"`; Recommendation string `json:"recommendation"` }
type Command struct { Executable string `json:"executable"`; Args []string `json:"args"`; WorkDir string `json:"work_dir"`; TimeoutSeconds int `json:"timeout_seconds"`; Purpose string `json:"purpose"` }
type Patch struct { Path string `json:"path"`; UnifiedDiff string `json:"unified_diff"`; BeforeHash string `json:"before_hash"` }
type ExecutionPlan struct { Version int `json:"version"`; Patches []Patch `json:"patches"`; Commands []Command `json:"commands"`; Acceptance []string `json:"acceptance"`; Recovery []string `json:"recovery"` }
type StepResult struct { Kind string `json:"kind"`; Name string `json:"name"`; ExitCode int `json:"exit_code"`; Stdout string `json:"stdout"`; Stderr string `json:"stderr"`; DurationMillis int64 `json:"duration_millis"`; Status string `json:"status"` }
type VerificationReport struct { Passed bool `json:"passed"`; ActualDiff string `json:"actual_diff"`; Steps []StepResult `json:"steps"`; Acceptance map[string]string `json:"acceptance"`; ReviewFindings []string `json:"review_findings"` }
```

Persist `ArtifactRecord{ID, RunID, Type, SchemaVersion, ParentID, ContentHash, FilePath}`. Write JSON to `.data/artifacts/<run-id>/<artifact-id>.json` through a temporary file followed by atomic rename, then insert the index record. On load, verify the file hash before returning it.

Add these GORM records and repository operations:

```go
type ProviderProfileRecord struct { ID string `gorm:"primaryKey"`; Provider string; Model string; ParametersJSON []byte }
type WorkspaceRecord struct { ID string `gorm:"primaryKey"`; CanonicalRoot string; RunnerID string; PolicyJSON []byte }
type TaskRecord struct { ID string `gorm:"primaryKey"`; WorkspaceID string; Requirement string; ConstraintsJSON []byte; AcceptanceJSON []byte }
type SeatRecord struct { ID string `gorm:"primaryKey"`; RunID string `gorm:"index"`; ProviderProfileID string; Role string; ProposalAlias string }
type ModelInvocationRecord struct { ID string `gorm:"primaryKey"`; RunID string `gorm:"index"`; SeatID string; Stage string; ProviderRequestID string; InputTokens int64; OutputTokens int64; EstimatedCostMicros int64; DurationMillis int64; ErrorCode string }
```

`CouncilRepository` exposes `SaveProviderProfile`, `SaveWorkspace`, `SaveTask`, `SaveSeats`, and `RecordInvocation`. None accepts an API key parameter. `RecordInvocation` is called after every successful or failed provider call with a redacted error code, never a raw vendor response body.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/council/schema ./internal/storage/sqlite -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/council/schema internal/storage/sqlite
git commit -m "feat: add council schemas and artifact storage"
```

### Task 4: Generate independent proposals and anonymous peer reviews

**Files:**
- Create: `internal/council/anonymize_test.go`
- Create: `internal/council/anonymize.go`
- Create: `internal/council/engine_test.go`
- Create: `internal/council/engine.go`

- [ ] **Step 1: Write failing behavior tests**

Use three recording fake providers. Assert that `Propose` starts all three calls before any is released, each prompt contains the same `TaskBrief`, and no prompt contains another model's output. Then call `Review` and assert aliases are deterministic, vendor/model names are absent, and `ReviewerSeatID` never reviews its own proposal.

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/council -run 'Independent|Anonymous|SelfReview' -v`

Expected: FAIL because `Engine` is undefined.

- [ ] **Step 3: Implement seats, concurrent proposals, and review assignment**

```go
type Seat struct { ID, Provider, Model, Role string }
type Generated[T any] struct { Seat Seat; Value T; Usage provider.Usage }

type Engine struct {
	registry *provider.Registry
	store ArtifactStore
	limits Limits
}

func proposalAlias(index int) string { return "Proposal " + string(rune('A'+index)) }
```

Use `errgroup.WithContext` to call all proposer seats concurrently. Store results in seat-order, not completion-order. Build review assignments by rotating the proposal list one place for each reviewer and filtering the reviewer's own seat. Prompts must contain JSON schema instructions and anonymous proposal JSON only.

- [ ] **Step 4: Run Council tests**

Run: `go test -race ./internal/council -v`

Expected: PASS with no data races.

- [ ] **Step 5: Commit**

```powershell
git add internal/council go.mod go.sum
git commit -m "feat: add independent proposals and blind peer review"
```

### Task 5: Add quorum, budget, Judge, red-team, and plan generation

**Files:**
- Create: `internal/council/limits_test.go`
- Create: `internal/council/limits.go`
- Modify: `internal/council/engine_test.go`
- Modify: `internal/council/engine.go`

- [ ] **Step 1: Write failing limit and adjudication tests**

Cover these cases: two successful proposers satisfy a quorum of two; one does not; accumulated cost above the configured micros limit stops before the next stage; Judge is not one of the proposer seats; blocking red-team findings return to Judging; clean findings produce an `ExecutionPlan`; malformed JSON gets one repair attempt and then returns `ErrInvalidArtifact`.

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/council -run 'Quorum|Budget|Judge|RedTeam|Repair' -v`

Expected: FAIL because limits and later stages are not implemented.

- [ ] **Step 3: Implement limits and remaining stages**

```go
type Limits struct { Quorum int; MaxRounds int; MaxInputTokens int64; MaxOutputTokens int64; MaxCostMicros int64; Timeout time.Duration }
type Meter struct { InputTokens, OutputTokens, CostMicros int64 }
func (m Meter) Allows(next provider.Usage, l Limits) bool {
	return m.InputTokens+next.InputTokens <= l.MaxInputTokens &&
		m.OutputTokens+next.OutputTokens <= l.MaxOutputTokens &&
		m.CostMicros+next.EstimatedCostMicros <= l.MaxCostMicros
}
```

Add `Judge`, `RedTeam`, and `BuildExecutionPlan` engine methods. Each validates JSON into the exact schema type before persistence. A blocking red-team report may loop to Judge only while `round < MaxRounds`; otherwise pause the run with a bounded-failure artifact.

- [ ] **Step 4: Run all Phase 2 checks**

Run:

```powershell
go test -race ./internal/provider/... ./internal/council/... ./internal/storage/sqlite/...
go vet ./...
```

Expected: both commands exit 0.

- [ ] **Step 5: Commit**

```powershell
git add internal/council
git commit -m "feat: complete bounded council deliberation"
```

## Phase 2 completion gate

Run `go test -race ./...` and verify that provider tests make no external requests. Demonstrate one fake-provider run producing Proposal, PeerReview, CouncilDecision, RedTeamReport, and ExecutionPlan artifacts while no project file is writable through Council code.
