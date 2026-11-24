package council

import (
	"errors"
	"fmt"
	"time"

	"github.com/aicouncil/aicouncil/internal/council/roles"
	"github.com/aicouncil/aicouncil/internal/provider"
)

// ProviderBuilder constructs the runnable provider client for one resolved
// slot (the caller owns secret lookup and vendor construction).
type ProviderBuilder func(slot roles.ResolvedSlot) (provider.ModelProvider, error)

// WorkflowFromResolved turns a resolved role assignment into a runnable
// workflow. Providers are registered under their profile ID, so two profiles
// of the same vendor can fill different slots. Slots whose provider cannot
// be built (profile deleted, key removed) are dropped; resolution is
// re-checked at run start, and any slot that then fails at call time goes
// through the workflow's skip/failover handling.
func WorkflowFromResolved(resolved roles.Resolved, build ProviderBuilder) (*Workflow, error) {
	built := map[string]provider.ModelProvider{}
	buildable := func(slot roles.ResolvedSlot) bool {
		if slot.ProviderProfileID == "" {
			return false
		}
		if _, ok := built[slot.ProviderProfileID]; ok {
			return true
		}
		client, err := build(slot)
		if err != nil || client == nil {
			return false
		}
		built[slot.ProviderProfileID] = provider.Named(slot.ProviderProfileID, client)
		return true
	}
	filter := func(slots []roles.ResolvedSlot) []roles.ResolvedSlot {
		kept := make([]roles.ResolvedSlot, 0, len(slots))
		for _, slot := range slots {
			if buildable(slot) {
				kept = append(kept, slot)
			}
		}
		return kept
	}
	proposers := filter(resolved.Proposers)
	reviewers := filter(resolved.Reviewers)
	judges := filter(resolved.Judge)
	redTeam := filter(resolved.RedTeam)
	if len(proposers) == 0 || len(judges) == 0 {
		return nil, errors.New("resolved council has no runnable proposer or judge seat")
	}
	items := make([]provider.ModelProvider, 0, len(built))
	for _, client := range built {
		items = append(items, client)
	}
	registry := provider.NewRegistry(items...)
	engine := NewEngine(registry, nil, Limits{Timeout: 2 * time.Minute})
	seat := func(slot roles.ResolvedSlot) Seat {
		return Seat{
			ID:          fmt.Sprintf("%s:%s:%d", slot.Role, slot.ProviderProfileID, slot.Ordinal),
			Provider:    slot.ProviderProfileID,
			Model:       slot.Model,
			Role:        slot.Role,
			ProfileID:   slot.ProviderProfileID,
			Ordinal:     slot.Ordinal,
			Temperature: slot.Temperature,
		}
	}
	seats := func(slots []roles.ResolvedSlot) []Seat {
		out := make([]Seat, 0, len(slots))
		for _, slot := range slots {
			out = append(out, seat(slot))
		}
		return out
	}
	var redPrimary Seat
	var redFailovers []Seat
	if len(redTeam) > 0 {
		redPrimary = seat(redTeam[0])
		redFailovers = seats(redTeam[1:])
	}
	workflow := NewWorkflow(engine, seats(proposers), seats(reviewers), seat(judges[0]), redPrimary)
	workflow.WithFailovers(seats(judges[1:]), redFailovers)
	workflow.SetCouncilSnapshot(resolved.SnapshotJSON())
	return workflow, nil
}
