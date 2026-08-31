package council

import (
	"context"
	"encoding/json"
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
