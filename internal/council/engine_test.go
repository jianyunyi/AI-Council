package council

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/provider"
	"github.com/stretchr/testify/require"
)

type recordingProvider struct {
	name     string
	release  <-chan struct{}
	mu       sync.Mutex
	requests []provider.Request
}

func (p *recordingProvider) Name() string { return p.name }
func (p *recordingProvider) Generate(_ context.Context, req provider.Request) (provider.Response, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	if p.release != nil {
		<-p.release
	}
	body, _ := json.Marshal(schema.Proposal{ID: p.name, Summary: "independent"})
	return provider.Response{Content: body, Usage: provider.Usage{InputTokens: 1, OutputTokens: 1}}, nil
}

func TestProposeStartsAllSeatsConcurrentlyAndKeepsPromptsIndependent(t *testing.T) {
	release := make(chan struct{})
	providers := []*recordingProvider{{name: "one", release: release}, {name: "two", release: release}, {name: "three", release: release}}
	reg := provider.NewRegistry(providers[0], providers[1], providers[2])
	e := NewEngine(reg, nil, Limits{Quorum: 2, Timeout: time.Second})
	result := make(chan []Generated[schema.Proposal], 1)
	go func() {
		got, _ := e.Propose(context.Background(), schema.TaskBrief{Requirement: "same requirement"}, []Seat{{ID: "s1", Provider: "one", Model: "m1"}, {ID: "s2", Provider: "two", Model: "m2"}, {ID: "s3", Provider: "three", Model: "m3"}})
		result <- got
	}()
	deadline := time.After(time.Second)
	for {
		count := 0
		for _, p := range providers {
			p.mu.Lock()
			count += len(p.requests)
			p.mu.Unlock()
		}
		if count == 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("providers did not start concurrently")
		case <-time.After(time.Millisecond):
		}
	}
	close(release)
	proposals := <-result
	require.Len(t, proposals, 3)
	for i, p := range providers {
		p.mu.Lock()
		req := p.requests[0]
		p.mu.Unlock()
		require.Contains(t, req.Messages[0].Content, "same requirement")
		require.NotContains(t, req.Messages[0].Content, providers[(i+1)%3].name+" output")
		require.Equal(t, "Proposal "+string(rune('A'+i)), proposals[i].Seat.ProposalAlias)
	}
}

func TestReviewIsBlindAndNeverSelfReviews(t *testing.T) {
	providers := []*recordingProvider{{name: "one"}, {name: "two"}, {name: "three"}}
	reg := provider.NewRegistry(providers[0], providers[1], providers[2])
	e := NewEngine(reg, nil, Limits{})
	proposals := []Generated[schema.Proposal]{
		{Seat: Seat{ID: "s1", Provider: "one", Model: "m1", ProposalAlias: "Proposal A"}, Value: schema.Proposal{Summary: "alpha"}},
		{Seat: Seat{ID: "s2", Provider: "two", Model: "m2", ProposalAlias: "Proposal B"}, Value: schema.Proposal{Summary: "beta"}},
		{Seat: Seat{ID: "s3", Provider: "three", Model: "m3", ProposalAlias: "Proposal C"}, Value: schema.Proposal{Summary: "gamma"}},
	}
	reviews, err := e.Review(context.Background(), proposals, []Seat{{ID: "s1", Provider: "one", Model: "m1"}, {ID: "s2", Provider: "two", Model: "m2"}, {ID: "s3", Provider: "three", Model: "m3"}})
	require.NoError(t, err)
	require.Len(t, reviews, 6)
	for _, review := range reviews {
		require.NotEqual(t, review.Seat.ID, review.Value.ProposalAlias)
		require.NotContains(t, review.Value.ProposalAlias, "one")
	}
	for _, p := range providers {
		p.mu.Lock()
		for _, req := range p.requests {
			require.NotContains(t, req.Messages[0].Content, `"Provider":"one"`)
			require.NotContains(t, req.Messages[0].Content, `"Provider":"two"`)
		}
		p.mu.Unlock()
	}
}

func TestBuildExecutionPlanRejectsEmptyDecisionPlan(t *testing.T) {
	_, err := BuildExecutionPlan(schema.CouncilDecision{Plan: schema.ExecutionPlan{Version: 1}}, schema.RedTeamReport{}, []string{"task acceptance"})
	require.Error(t, err)
}

func TestBuildExecutionPlanUsesValidatedDecisionPlanAndTaskAcceptance(t *testing.T) {
	decision := schema.CouncilDecision{Plan: schema.ExecutionPlan{
		Version:              1,
		Patches:              []schema.Patch{{Path: "internal/example.go", UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n"}},
		Commands:             []schema.Command{{Executable: "go", Args: []string{"test", "./..."}, TimeoutSeconds: 30}},
		VerificationCommands: []schema.Command{{Executable: "go", Args: []string{"vet", "./..."}, TimeoutSeconds: 30}},
		Acceptance:           []string{"judge acceptance"},
	}}

	got, err := BuildExecutionPlan(decision, schema.RedTeamReport{}, []string{"task acceptance"})
	require.NoError(t, err)
	require.Equal(t, decision.Plan.Patches, got.Patches)
	require.Equal(t, decision.Plan.Commands, got.Commands)
	require.Equal(t, decision.Plan.VerificationCommands, got.VerificationCommands)
	require.Equal(t, []string{"task acceptance"}, got.Acceptance)
}

func TestBuildExecutionPlanRejectsRedTeamBlockers(t *testing.T) {
	decision := schema.CouncilDecision{Plan: schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "go", TimeoutSeconds: 30}}}}
	_, err := BuildExecutionPlan(decision, schema.RedTeamReport{Blocking: []string{"unsafe"}}, nil)
	require.Error(t, err)
}

func TestProposeIncludesWorkspaceFilesInPrompt(t *testing.T) {
	p := &recordingProvider{name: "one"}
	e := NewEngine(provider.NewRegistry(p), nil, Limits{})
	_, err := e.Propose(context.Background(), schema.TaskBrief{
		Requirement:    "add a test",
		WorkspaceFiles: []schema.WorkspaceFile{{Path: "internal/example.go", Content: "package example"}},
	}, []Seat{{ID: "s1", Provider: "one", Model: "m1"}})
	require.NoError(t, err)
	p.mu.Lock()
	defer p.mu.Unlock()
	require.Contains(t, p.requests[0].Messages[0].Content, "internal/example.go")
	require.Contains(t, p.requests[0].Messages[0].Content, "package example")
}

func TestProposeRejectsWorkspaceFilesOutsideContextEnvelopeBeforeProvider(t *testing.T) {
	maxFileBytes := 64 << 10
	cases := []struct {
		name  string
		files []schema.WorkspaceFile
	}{
		{
			name:  "more than 200 files",
			files: make([]schema.WorkspaceFile, 201),
		},
		{
			name:  "file content larger than 64 KiB",
			files: []schema.WorkspaceFile{{Path: "large.go", Content: strings.Repeat("x", maxFileBytes+1)}},
		},
		{
			name: "combined UTF-8 content larger than 2 MiB",
			files: func() []schema.WorkspaceFile {
				files := make([]schema.WorkspaceFile, 33)
				for i := range files {
					files[i] = schema.WorkspaceFile{Path: "file.go", Content: strings.Repeat("x", maxFileBytes)}
				}
				return files
			}(),
		},
		{
			name:  "absolute workspace path",
			files: []schema.WorkspaceFile{{Path: `C:\\outside.go`, Content: "package outside"}},
		},
		{
			name:  "workspace path traversal",
			files: []schema.WorkspaceFile{{Path: `..\\outside.go`, Content: "package outside"}},
		},
		{
			name:  "invalid UTF-8 workspace content",
			files: []schema.WorkspaceFile{{Path: "internal/example.go", Content: string([]byte{0xff})}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &recordingProvider{name: "one"}
			e := NewEngine(provider.NewRegistry(p), nil, Limits{})

			_, err := e.Propose(context.Background(), schema.TaskBrief{WorkspaceFiles: tc.files}, []Seat{{ID: "s1", Provider: "one", Model: "m1"}})

			require.Error(t, err)
			p.mu.Lock()
			defer p.mu.Unlock()
			require.Empty(t, p.requests)
		})
	}
}

func TestBuildExecutionPlanRejectsInvalidPlanFields(t *testing.T) {
	validCommand := schema.Command{Executable: "go", TimeoutSeconds: 30}
	cases := []struct {
		name string
		plan schema.ExecutionPlan
	}{
		{name: "nonpositive version", plan: schema.ExecutionPlan{Version: 0, Commands: []schema.Command{validCommand}}},
		{name: "absolute patch path", plan: schema.ExecutionPlan{Version: 1, Patches: []schema.Patch{{Path: `C:\\outside.go`}}}},
		{name: "traversal patch path", plan: schema.ExecutionPlan{Version: 1, Patches: []schema.Patch{{Path: "../outside.go"}}}},
		{name: "empty patch path", plan: schema.ExecutionPlan{Version: 1, Patches: []schema.Patch{{Path: " "}}}},
		{name: "blank command executable", plan: schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: " ", TimeoutSeconds: 30}}}},
		{name: "nonpositive command timeout", plan: schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "go", TimeoutSeconds: 0}}}},
		{name: "blank verification executable", plan: schema.ExecutionPlan{Version: 1, Commands: []schema.Command{validCommand}, VerificationCommands: []schema.Command{{Executable: " ", TimeoutSeconds: 30}}}},
		{name: "nonpositive verification timeout", plan: schema.ExecutionPlan{Version: 1, Commands: []schema.Command{validCommand}, VerificationCommands: []schema.Command{{Executable: "go", TimeoutSeconds: -1}}}},
		{name: "absolute command work directory", plan: schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "go", TimeoutSeconds: 30, WorkDir: `C:\\outside`}}}},
		{name: "traversal command work directory", plan: schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "go", TimeoutSeconds: 30, WorkDir: `..\\outside`}}}},
		{name: "absolute verification work directory", plan: schema.ExecutionPlan{Version: 1, Commands: []schema.Command{validCommand}, VerificationCommands: []schema.Command{{Executable: "go", TimeoutSeconds: 30, WorkDir: `C:\\outside`}}}},
		{name: "traversal verification work directory", plan: schema.ExecutionPlan{Version: 1, Commands: []schema.Command{validCommand}, VerificationCommands: []schema.Command{{Executable: "go", TimeoutSeconds: 30, WorkDir: `..\\outside`}}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildExecutionPlan(schema.CouncilDecision{Plan: tc.plan}, schema.RedTeamReport{}, nil)
			require.Error(t, err)
		})
	}
}
