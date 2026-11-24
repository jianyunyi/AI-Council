package roles

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// Store is the persistence the resolver needs (satisfied by
// sqlite.CouncilRolesRepository).
type Store interface {
	LoadSet(ctx context.Context, ownerID, scopeType, scopeID string) (sqlite.CouncilRoleAssignmentSetRecord, []sqlite.CouncilRoleAssignmentRecord, bool, error)
}

// ProfileSource lists an account's provider profiles (never the keys).
type ProfileSource func(ctx context.Context, ownerID string) ([]ProfileInfo, error)

type Resolver struct {
	store    Store
	profiles ProfileSource
}

func NewResolver(store Store, profiles ProfileSource) *Resolver {
	return &Resolver{store: store, profiles: profiles}
}

// ResolvedSlot is one runnable seat: the profile behind it, the vendor name
// (for provider construction) and the model to call.
type ResolvedSlot struct {
	Role              string          `json:"role"`
	Ordinal           int             `json:"ordinal"`
	ProviderProfileID string          `json:"provider_profile_id"`
	Provider          string          `json:"provider"`
	Model             string          `json:"model"`
	Params            json.RawMessage `json:"params,omitempty"`
	Temperature       *float64        `json:"-"`
}

// Resolved is the effective council configuration for one run.
type Resolved struct {
	Source    string         `json:"source"`
	Version   int            `json:"version"`
	Proposers []ResolvedSlot `json:"proposers"`
	Reviewers []ResolvedSlot `json:"reviewers"`
	Judge     []ResolvedSlot `json:"judge"`    // primary first, then failovers
	RedTeam   []ResolvedSlot `json:"red_team"` // primary first, then failovers
}

// Resolve applies the precedence chain for (owner, workspace): the
// workspace's own set wins, then the account's global set, then the legacy
// default derived from the account's keyed provider profiles.
func (r *Resolver) Resolve(ctx context.Context, ownerID, workspaceID string) (Resolved, error) {
	profiles, err := r.profiles(ctx, ownerID)
	if err != nil {
		return Resolved{}, err
	}
	if r.store != nil && workspaceID != "" {
		set, records, found, err := r.store.LoadSet(ctx, ownerID, ScopeWorkspace, workspaceID)
		if err != nil {
			return Resolved{}, err
		}
		if found {
			return resolvedFromRecords(set, records, profiles, SourceWorkspace), nil
		}
	}
	if r.store != nil {
		set, records, found, err := r.store.LoadSet(ctx, ownerID, ScopeGlobal, "")
		if err != nil {
			return Resolved{}, err
		}
		if found {
			return resolvedFromRecords(set, records, profiles, SourceGlobal), nil
		}
	}
	return LegacyDefault(profiles), nil
}

// EffectiveSource reports where resolution for (owner, workspace) lands,
// without building the full seat lists.
func (r *Resolver) EffectiveSource(ctx context.Context, ownerID, workspaceID string) (string, error) {
	if r.store != nil && workspaceID != "" {
		_, _, found, err := r.store.LoadSet(ctx, ownerID, ScopeWorkspace, workspaceID)
		if err != nil {
			return "", err
		}
		if found {
			return SourceWorkspace, nil
		}
	}
	if r.store != nil {
		_, _, found, err := r.store.LoadSet(ctx, ownerID, ScopeGlobal, "")
		if err != nil {
			return "", err
		}
		if found {
			return SourceGlobal, nil
		}
	}
	return SourceLegacy, nil
}

func resolvedFromRecords(set sqlite.CouncilRoleAssignmentSetRecord, records []sqlite.CouncilRoleAssignmentRecord, profiles []ProfileInfo, source string) Resolved {
	byID := make(map[string]ProfileInfo, len(profiles))
	for _, profile := range profiles {
		byID[profile.ID] = profile
	}
	resolved := Resolved{Source: source, Version: set.Version}
	byRole := map[string][]ResolvedSlot{}
	for _, record := range records {
		if !record.Enabled {
			continue
		}
		slot := ResolvedSlot{
			Role: record.Role, Ordinal: record.Ordinal,
			ProviderProfileID: record.ProviderProfileID, Model: record.Model,
			Params: json.RawMessage(record.ParamsJSON),
		}
		if profile, ok := byID[record.ProviderProfileID]; ok {
			slot.Provider = profile.Provider
		}
		slot.Temperature = Temperature(slot.Params)
		byRole[record.Role] = append(byRole[record.Role], slot)
	}
	for role := range byRole {
		sort.Slice(byRole[role], func(i, j int) bool { return byRole[role][i].Ordinal < byRole[role][j].Ordinal })
	}
	resolved.Proposers = byRole[RoleProposer]
	resolved.Reviewers = byRole[RoleReviewer]
	resolved.Judge = byRole[RoleJudge]
	resolved.RedTeam = byRole[RoleRedTeam]
	return resolved
}

// providerOrder is the fixed vendor order the pre-assignment factory used
// when building seats (openai, deepseek, anthropic). The legacy default
// keeps it exactly, so judge/red-team pick the same first provider.
var providerOrder = []string{"openai", "deepseek", "anthropic"}

// LegacyDefault reproduces the v0.2.1 seat composition from the account's
// profiles: one seat per provider (the first keyed profile per provider, in
// profile-list order), every seat proposes and reviews, and the first
// provider's seat judges and red-teams.
func LegacyDefault(profiles []ProfileInfo) Resolved {
	resolved := Resolved{Source: SourceLegacy, Version: 0}
	firstByProvider := map[string]ProfileInfo{}
	var encounter []string
	for _, profile := range profiles {
		if !profile.HasKey {
			continue
		}
		if _, seen := firstByProvider[profile.Provider]; seen {
			continue
		}
		firstByProvider[profile.Provider] = profile
		encounter = append(encounter, profile.Provider)
	}
	ordered := make([]string, 0, len(encounter))
	for _, name := range providerOrder {
		if _, ok := firstByProvider[name]; ok {
			ordered = append(ordered, name)
		}
	}
	for _, name := range encounter {
		found := false
		for _, known := range providerOrder {
			if name == known {
				found = true
			}
		}
		if !found {
			ordered = append(ordered, name)
		}
	}
	var seats []ResolvedSlot
	for i, name := range ordered {
		profile := firstByProvider[name]
		seats = append(seats, ResolvedSlot{
			Role: RoleProposer, Ordinal: i,
			ProviderProfileID: profile.ID, Provider: profile.Provider, Model: profile.Model,
		})
	}
	resolved.Proposers = seats
	resolved.Reviewers = append([]ResolvedSlot(nil), seats...)
	if len(seats) > 0 {
		judge := seats[0]
		judge.Role = RoleJudge
		judge.Ordinal = 0
		resolved.Judge = []ResolvedSlot{judge}
		redTeam := seats[0]
		redTeam.Role = RoleRedTeam
		redTeam.Ordinal = 0
		resolved.RedTeam = []ResolvedSlot{redTeam}
	}
	return resolved
}

// SnapshotJSON is the immutable record of the resolved configuration that a
// run started with (stored on the run row at start).
func (r Resolved) SnapshotJSON() []byte {
	type snapshot struct {
		Source     string         `json:"source"`
		Version    int            `json:"version"`
		ResolvedAt time.Time      `json:"resolved_at"`
		Proposers  []ResolvedSlot `json:"proposers"`
		Reviewers  []ResolvedSlot `json:"reviewers"`
		Judge      []ResolvedSlot `json:"judge"`
		RedTeam    []ResolvedSlot `json:"red_team"`
	}
	raw, _ := json.Marshal(snapshot{
		Source: r.Source, Version: r.Version, ResolvedAt: time.Now().UTC(),
		Proposers: r.Proposers, Reviewers: r.Reviewers, Judge: r.Judge, RedTeam: r.RedTeam,
	})
	return raw
}
