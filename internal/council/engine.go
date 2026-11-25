package council

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	pathpkg "path"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aicouncil/aicouncil/internal/core/artifact"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/provider"
)

var ErrInvalidArtifact = errors.New("invalid council artifact")

const (
	maxWorkspaceFiles      = 200
	maxWorkspaceFileBytes  = 64 << 10
	maxWorkspaceTotalBytes = 2 << 20
)

type Seat struct {
	ID, Provider, Model, Role, ProposalAlias string
	// ProfileID and Ordinal identify the assignment slot the seat came from
	// (empty for seats built by the legacy static configuration).
	ProfileID string
	Ordinal   int
	// Temperature, when set, is forwarded to the provider request.
	Temperature *float64
}

func seatTemperature(seat Seat) float64 {
	if seat.Temperature == nil {
		return 0
	}
	return *seat.Temperature
}

type Generated[T any] struct {
	Seat  Seat
	Value T
	Usage provider.Usage
}
type ArtifactStore interface {
	Save(context.Context, string, artifact.Envelope) error
}

type Engine struct {
	registry *provider.Registry
	store    ArtifactStore
	limits   Limits
}

func NewEngine(registry *provider.Registry, store ArtifactStore, limits Limits) *Engine {
	return &Engine{registry: registry, store: store, limits: limits}
}

// SeatFailure records one seat that could not produce a result, for the
// lenient engine variants that drop failed seats instead of failing the run.
type SeatFailure struct {
	Seat Seat
	Err  error
}

func (e *Engine) Propose(ctx context.Context, brief schema.TaskBrief, seats []Seat) ([]Generated[schema.Proposal], error) {
	results, failures, err := e.ProposeWithFailures(ctx, brief, seats)
	if err != nil {
		return nil, err
	}
	if len(failures) > 0 {
		return nil, failures[0].Err
	}
	return results, nil
}

// ProposeWithFailures runs every proposer seat and keeps the successes. A
// seat that errors (provider down, invalid output) is reported as a failure
// and dropped; the call only errors when no proposal survives or the brief
// itself is invalid.
func (e *Engine) ProposeWithFailures(ctx context.Context, brief schema.TaskBrief, seats []Seat) ([]Generated[schema.Proposal], []SeatFailure, error) {
	if e.registry == nil {
		return nil, nil, errors.New("provider registry is required")
	}
	if len(seats) == 0 {
		return nil, nil, errors.New("at least one proposer seat is required")
	}
	if err := validateWorkspaceFiles(brief.WorkspaceFiles); err != nil {
		return nil, nil, err
	}
	ctx, cancel := withTimeout(ctx, e.limits.Timeout)
	defer cancel()
	briefJSON, _ := json.Marshal(brief)
	schemaJSON, _ := json.Marshal(schema.Proposal{})
	results := make([]Generated[schema.Proposal], len(seats))
	errs := make([]error, len(seats))
	var wg sync.WaitGroup
	for i := range seats {
		i := i
		seat := seats[i]
		seat.ProposalAlias = proposalAlias(i)
		results[i].Seat = seat
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, ok := e.registry.Get(seat.Provider)
			if !ok {
				errs[i] = fmt.Errorf("provider %q not found", seat.Provider)
				return
			}
			resp, err := p.Generate(ctx, provider.Request{Model: seat.Model, Temperature: seatTemperature(seat), Messages: []provider.Message{{Role: "user", Content: fmt.Sprintf("Task brief JSON:\n%s\nReturn only JSON matching this schema:\n%s", briefJSON, schemaJSON)}}})
			if err != nil {
				errs[i] = err
				return
			}
			var proposal schema.Proposal
			if json.Unmarshal(resp.Content, &proposal) != nil {
				errs[i] = ErrInvalidArtifact
				return
			}
			results[i].Value = proposal
			results[i].Usage = resp.Usage
		}()
	}
	wg.Wait()
	kept := make([]Generated[schema.Proposal], 0, len(seats))
	var failures []SeatFailure
	for i, err := range errs {
		if err != nil {
			failures = append(failures, SeatFailure{Seat: results[i].Seat, Err: err})
			continue
		}
		kept = append(kept, results[i])
	}
	if len(kept) == 0 {
		return nil, failures, errors.New("all proposer seats failed")
	}
	return kept, failures, nil
}

func (e *Engine) Review(ctx context.Context, proposals []Generated[schema.Proposal], reviewers []Seat) ([]Generated[schema.PeerReview], error) {
	results, failures, err := e.ReviewWithFailures(ctx, proposals, reviewers)
	if err != nil {
		return nil, err
	}
	if len(failures) > 0 {
		return nil, failures[0].Err
	}
	return results, nil
}

// ReviewWithFailures is the lenient Review: a reviewer seat that fails is
// dropped and reported; the call only errors when no review survives.
func (e *Engine) ReviewWithFailures(ctx context.Context, proposals []Generated[schema.Proposal], reviewers []Seat) ([]Generated[schema.PeerReview], []SeatFailure, error) {
	if len(proposals) == 0 || len(reviewers) == 0 {
		return nil, nil, errors.New("proposals and reviewers are required")
	}
	ctx, cancel := withTimeout(ctx, e.limits.Timeout)
	defer cancel()
	type reviewerOutcome struct {
		results []Generated[schema.PeerReview]
		err     error
	}
	outcomes := make([]reviewerOutcome, len(reviewers))
	var wg sync.WaitGroup
	for ri, reviewer := range reviewers {
		ri, reviewer := ri, reviewer
		wg.Add(1)
		go func() {
			defer wg.Done()
			outcome := &outcomes[ri]
			model, ok := e.registry.Get(reviewer.Provider)
			if !ok {
				outcome.err = fmt.Errorf("provider %q not found", reviewer.Provider)
				return
			}
			for offset := 1; offset <= len(proposals); offset++ {
				pi := (ri + offset) % len(proposals)
				target := proposals[pi]
				if target.Seat.ID == reviewer.ID {
					continue
				}
				anonymous := map[string]any{"alias": proposalAlias(pi), "proposal": target.Value}
				payload, _ := json.Marshal(anonymous)
				schemaJSON, _ := json.Marshal(schema.PeerReview{})
				resp, err := model.Generate(ctx, provider.Request{Model: reviewer.Model, Temperature: seatTemperature(reviewer), Messages: []provider.Message{{Role: "user", Content: fmt.Sprintf("Review the anonymous proposal JSON below. Do not infer vendor or model identity.\n%s\nReturn only JSON matching this schema:\n%s", payload, schemaJSON)}}})
				if err != nil {
					outcome.err = err
					return
				}
				var value schema.PeerReview
				if json.Unmarshal(resp.Content, &value) != nil {
					outcome.err = ErrInvalidArtifact
					return
				}
				value.ReviewerSeatID, value.ProposalAlias = reviewer.ID, proposalAlias(pi)
				outcome.results = append(outcome.results, Generated[schema.PeerReview]{Seat: reviewer, Value: value, Usage: resp.Usage})
			}
		}()
	}
	wg.Wait()
	var results []Generated[schema.PeerReview]
	var failures []SeatFailure
	for ri, outcome := range outcomes {
		if outcome.err != nil {
			failures = append(failures, SeatFailure{Seat: reviewers[ri], Err: outcome.err})
			continue
		}
		results = append(results, outcome.results...)
	}
	if len(results) == 0 {
		return nil, failures, errors.New("all reviewer seats failed")
	}
	return results, failures, nil
}

func (e *Engine) Judge(ctx context.Context, proposals []Generated[schema.Proposal], reviews []Generated[schema.PeerReview], judge Seat) (Generated[schema.CouncilDecision], error) {
	for _, p := range proposals {
		if p.Seat.ID == judge.ID {
			return Generated[schema.CouncilDecision]{}, errors.New("judge seat must not be a proposer")
		}
	}
	p, ok := e.registry.Get(judge.Provider)
	if !ok {
		return Generated[schema.CouncilDecision]{}, fmt.Errorf("provider %q not found", judge.Provider)
	}
	payload, _ := json.Marshal(map[string]any{"proposals": proposals, "reviews": reviews})
	schemaJSON, _ := json.Marshal(schema.CouncilDecision{})
	resp, err := p.Generate(ctx, provider.Request{Model: judge.Model, Temperature: seatTemperature(judge), Messages: []provider.Message{{Role: "user", Content: fmt.Sprintf("Select the best proposals from this council record:\n%s\nReturn only JSON matching this schema:\n%s", payload, schemaJSON)}}})
	if err != nil {
		return Generated[schema.CouncilDecision]{}, err
	}
	var decision schema.CouncilDecision
	if json.Unmarshal(resp.Content, &decision) != nil {
		resp, err = p.Generate(ctx, provider.Request{Model: judge.Model, Temperature: seatTemperature(judge), Messages: []provider.Message{{Role: "user", Content: "Repair the previous response into valid JSON only for the CouncilDecision schema."}}})
		if err != nil || json.Unmarshal(resp.Content, &decision) != nil {
			return Generated[schema.CouncilDecision]{}, ErrInvalidArtifact
		}
	}
	return Generated[schema.CouncilDecision]{Seat: judge, Value: decision, Usage: resp.Usage}, nil
}

func (e *Engine) RedTeam(ctx context.Context, decision schema.CouncilDecision, redTeam Seat) (Generated[schema.RedTeamReport], error) {
	p, ok := e.registry.Get(redTeam.Provider)
	if !ok {
		return Generated[schema.RedTeamReport]{}, fmt.Errorf("provider %q not found", redTeam.Provider)
	}
	payload, _ := json.Marshal(decision)
	schemaJSON, _ := json.Marshal(schema.RedTeamReport{})
	message := fmt.Sprintf("Find blocking risks in this decision:\n%s\nReturn only JSON matching this schema:\n%s", payload, schemaJSON)
	resp, err := p.Generate(ctx, provider.Request{Model: redTeam.Model, Temperature: seatTemperature(redTeam), Messages: []provider.Message{{Role: "user", Content: message}}})
	if err != nil {
		return Generated[schema.RedTeamReport]{}, err
	}
	var report schema.RedTeamReport
	if json.Unmarshal(resp.Content, &report) != nil {
		return Generated[schema.RedTeamReport]{}, ErrInvalidArtifact
	}
	return Generated[schema.RedTeamReport]{Seat: redTeam, Value: report, Usage: resp.Usage}, nil
}

func BuildExecutionPlan(decision schema.CouncilDecision, report schema.RedTeamReport, acceptance []string) (schema.ExecutionPlan, error) {
	plan := decision.Plan
	if issues := ValidatePlan(plan, report); len(issues) > 0 {
		return schema.ExecutionPlan{}, errors.New(issues[0].Message)
	}
	plan.Acceptance = append([]string(nil), acceptance...)
	return plan, nil
}

func validateWorkspaceFiles(files []schema.WorkspaceFile) error {
	if len(files) > maxWorkspaceFiles {
		return errors.New("workspace context exceeds maximum file count")
	}
	var total int64
	for _, file := range files {
		if !isSafeRelativePath(file.Path) {
			return errors.New("workspace context file path must be a nonempty relative path")
		}
		if !utf8.ValidString(file.Content) {
			return errors.New("workspace context file content must be valid UTF-8")
		}
		contentBytes := int64(len(file.Content))
		if contentBytes > maxWorkspaceFileBytes {
			return errors.New("workspace context contains a file that exceeds the maximum size")
		}
		total += contentBytes
		if total > maxWorkspaceTotalBytes {
			return errors.New("workspace context exceeds the maximum total size")
		}
	}
	return nil
}

func isSafeRelativePath(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !filepath.IsAbs(value) && !pathpkg.IsAbs(value) && !hasWindowsVolume(value) && filepath.VolumeName(value) == "" && !hasTraversalComponent(value)
}

func hasWindowsVolume(value string) bool {
	if len(value) >= 2 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' {
		return true
	}
	return strings.HasPrefix(value, `\\`)
}

func isSafeWorkDir(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || value == "." || isSafeRelativePath(value)
}

func hasTraversalComponent(path string) bool {
	for _, component := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if component == ".." {
			return true
		}
	}
	return false
}

func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}
