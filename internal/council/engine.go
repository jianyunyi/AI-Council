package council

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aicouncil/aicouncil/internal/core/artifact"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/provider"
)

var ErrInvalidArtifact = errors.New("invalid council artifact")

type Seat struct{ ID, Provider, Model, Role, ProposalAlias string }
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

func (e *Engine) Propose(ctx context.Context, brief schema.TaskBrief, seats []Seat) ([]Generated[schema.Proposal], error) {
	if e.registry == nil {
		return nil, errors.New("provider registry is required")
	}
	if len(seats) == 0 {
		return nil, errors.New("at least one proposer seat is required")
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
			resp, err := p.Generate(ctx, provider.Request{Model: seat.Model, Messages: []provider.Message{{Role: "user", Content: fmt.Sprintf("Task brief JSON:\n%s\nReturn only JSON matching this schema:\n%s", briefJSON, schemaJSON)}}})
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
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}

func (e *Engine) Review(ctx context.Context, proposals []Generated[schema.Proposal], reviewers []Seat) ([]Generated[schema.PeerReview], error) {
	if len(proposals) == 0 || len(reviewers) == 0 {
		return nil, errors.New("proposals and reviewers are required")
	}
	ctx, cancel := withTimeout(ctx, e.limits.Timeout)
	defer cancel()
	results := make([]Generated[schema.PeerReview], 0, len(reviewers)*len(proposals))
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	for ri, reviewer := range reviewers {
		ri, reviewer := ri, reviewer
		wg.Add(1)
		go func() {
			defer wg.Done()
			model, ok := e.registry.Get(reviewer.Provider)
			if !ok {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("provider %q not found", reviewer.Provider)
				}
				mu.Unlock()
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
				resp, err := model.Generate(ctx, provider.Request{Model: reviewer.Model, Messages: []provider.Message{{Role: "user", Content: fmt.Sprintf("Review the anonymous proposal JSON below. Do not infer vendor or model identity.\n%s\nReturn only JSON matching this schema:\n%s", payload, schemaJSON)}}})
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
				var value schema.PeerReview
				if json.Unmarshal(resp.Content, &value) != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = ErrInvalidArtifact
					}
					mu.Unlock()
					return
				}
				value.ReviewerSeatID, value.ProposalAlias = reviewer.ID, proposalAlias(pi)
				mu.Lock()
				results = append(results, Generated[schema.PeerReview]{Seat: reviewer, Value: value, Usage: resp.Usage})
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
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
	resp, err := p.Generate(ctx, provider.Request{Model: judge.Model, Messages: []provider.Message{{Role: "user", Content: fmt.Sprintf("Select the best proposals from this council record:\n%s\nReturn only JSON matching this schema:\n%s", payload, schemaJSON)}}})
	if err != nil {
		return Generated[schema.CouncilDecision]{}, err
	}
	var decision schema.CouncilDecision
	if json.Unmarshal(resp.Content, &decision) != nil {
		resp, err = p.Generate(ctx, provider.Request{Model: judge.Model, Messages: []provider.Message{{Role: "user", Content: "Repair the previous response into valid JSON only for the CouncilDecision schema."}}})
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
	resp, err := p.Generate(ctx, provider.Request{Model: redTeam.Model, Messages: []provider.Message{{Role: "user", Content: message}}})
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
	if len(report.Blocking) > 0 {
		return schema.ExecutionPlan{}, errors.New("red-team report contains blocking findings")
	}
	plan := schema.ExecutionPlan{Version: 1, Acceptance: append([]string(nil), acceptance...), Recovery: []string{"restore workspace snapshot if verification fails"}}
	return plan, nil
}

func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}
