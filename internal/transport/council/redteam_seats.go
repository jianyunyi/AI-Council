package council

import (
	"context"
	"fmt"

	"github.com/aicouncil/aicouncil/internal/app/group"
	"github.com/aicouncil/aicouncil/internal/council/roles"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// redTeamSeatAdapter wraps the Part A roles Resolver (W13, as landed:
// Resolve(ctx, ownerID, workspaceID) with Resolved.RedTeam as a field
// of ResolvedSlot{ProviderProfileID, Provider, Model, Ordinal,
// Temperature} — there is no Slots() method and no ProfileID field).
// Seats are resolved for the GROUP's owner over the group's shared
// workspace, so workspace-scoped assignments win over global ones
// exactly as they do for council runs. Primary first, then failovers.
type redTeamSeatAdapter struct {
	api      *API
	resolver *roles.Resolver
}

func (r redTeamSeatAdapter) RedTeamSeats(ctx context.Context, groupID string) ([]group.RedTeamSeat, error) {
	if r.api.db == nil || r.resolver == nil {
		return nil, fmt.Errorf("red-team seat resolution is not configured")
	}
	var g sqlite.ChatGroupRecord
	if err := r.api.db.WithContext(ctx).Where("id = ?", groupID).First(&g).Error; err != nil {
		return nil, fmt.Errorf("load group for red-team seats: %w", err)
	}
	res, err := r.resolver.Resolve(ctx, g.OwnerID, g.SharedWorkspaceID)
	if err != nil {
		return nil, err
	}
	out := make([]group.RedTeamSeat, 0, len(res.RedTeam))
	for _, s := range res.RedTeam {
		out = append(out, group.RedTeamSeat{
			ProfileID: s.ProviderProfileID, Model: s.Model,
			Ordinal: s.Ordinal, Temperature: s.Temperature,
		})
	}
	return out, nil
}
