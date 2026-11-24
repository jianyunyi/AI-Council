package roles

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func testProfiles() []ProfileInfo {
	return []ProfileInfo{
		{ID: "p-openai", Provider: "openai", Model: "gpt-4o", HasKey: true},
		{ID: "p-deepseek", Provider: "deepseek", Model: "deepseek-chat", HasKey: true},
		{ID: "p-anthropic", Provider: "anthropic", Model: "claude-sonnet", HasKey: true},
		{ID: "p-nokey", Provider: "openai", Model: "gpt-4o-mini", HasKey: false},
	}
}

func profileSource(profiles []ProfileInfo) ProfileSource {
	return func(context.Context, string) ([]ProfileInfo, error) { return profiles, nil }
}

func openRolesDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "roles.db"))
	require.NoError(t, err)
	return db
}

func slotRecord(role string, ordinal int, profileID, model string) sqlite.CouncilRoleAssignmentRecord {
	return sqlite.CouncilRoleAssignmentRecord{
		ID:   role + "-" + string(rune('a'+ordinal)) + "-" + profileID,
		Role: role, Ordinal: ordinal, ProviderProfileID: profileID, Model: model, Enabled: true,
	}
}

func validGlobalSet() []sqlite.CouncilRoleAssignmentRecord {
	return []sqlite.CouncilRoleAssignmentRecord{
		slotRecord(RoleProposer, 0, "p-openai", "gpt-4o"),
		slotRecord(RoleJudge, 0, "p-deepseek", "deepseek-chat"),
	}
}

func TestLegacyDefaultMatchesV021Composition(t *testing.T) {
	// Two keyed openai profiles: the first in list order wins, exactly like
	// the pre-assignment council factory.
	profiles := append([]ProfileInfo{
		{ID: "p-openai-new", Provider: "openai", Model: "gpt-4o", HasKey: true},
	}, testProfiles()...)
	resolved := LegacyDefault(profiles)
	require.Equal(t, SourceLegacy, resolved.Source)
	require.Len(t, resolved.Proposers, 3)
	require.Equal(t, "p-openai-new", resolved.Proposers[0].ProviderProfileID, "fixed vendor order: openai first")
	require.Equal(t, "p-deepseek", resolved.Proposers[1].ProviderProfileID)
	require.Equal(t, "p-anthropic", resolved.Proposers[2].ProviderProfileID)
	require.Equal(t, resolved.Proposers, resolved.Reviewers, "every seat reviews in the legacy default")
	require.Len(t, resolved.Judge, 1)
	require.Equal(t, "p-openai-new", resolved.Judge[0].ProviderProfileID, "first provider judges")
	require.Len(t, resolved.RedTeam, 1)
	require.Equal(t, resolved.Judge[0].ProviderProfileID, resolved.RedTeam[0].ProviderProfileID, "judge model red-teams")

	// Keyless profiles never appear.
	for _, slot := range resolved.Proposers {
		require.NotEqual(t, "p-nokey", slot.ProviderProfileID)
	}
}

func TestResolverPrecedenceWorkspaceOverGlobalOverLegacy(t *testing.T) {
	ctx := context.Background()
	db := openRolesDB(t)
	repo := sqlite.NewCouncilRolesRepository(db)
	resolver := NewResolver(repo, profileSource(testProfiles()))

	// No sets at all -> legacy.
	resolved, err := resolver.Resolve(ctx, "alice", "ws-1")
	require.NoError(t, err)
	require.Equal(t, SourceLegacy, resolved.Source)

	// Global set -> global wins over legacy.
	_, err = repo.ReplaceSet(ctx, "alice", ScopeGlobal, "", 0, validGlobalSet(), "alice")
	require.NoError(t, err)
	resolved, err = resolver.Resolve(ctx, "alice", "ws-1")
	require.NoError(t, err)
	require.Equal(t, SourceGlobal, resolved.Source)
	require.Equal(t, 1, resolved.Version)
	require.Len(t, resolved.Proposers, 1)
	require.Equal(t, "p-openai", resolved.Proposers[0].ProviderProfileID)
	require.Equal(t, "openai", resolved.Proposers[0].Provider)
	require.Len(t, resolved.Judge, 1)
	require.Equal(t, "p-deepseek", resolved.Judge[0].ProviderProfileID)

	// Workspace set -> workspace wins over global.
	workspaceSet := []sqlite.CouncilRoleAssignmentRecord{
		slotRecord(RoleProposer, 0, "p-anthropic", "claude-sonnet"),
		slotRecord(RoleJudge, 0, "p-anthropic", "claude-sonnet"),
	}
	_, err = repo.ReplaceSet(ctx, "alice", ScopeWorkspace, "ws-1", 0, workspaceSet, "alice")
	require.NoError(t, err)
	resolved, err = resolver.Resolve(ctx, "alice", "ws-1")
	require.NoError(t, err)
	require.Equal(t, SourceWorkspace, resolved.Source)
	require.Equal(t, "p-anthropic", resolved.Proposers[0].ProviderProfileID)

	// Another workspace still resolves to global.
	resolved, err = resolver.Resolve(ctx, "alice", "ws-2")
	require.NoError(t, err)
	require.Equal(t, SourceGlobal, resolved.Source)

	// Deleting the workspace set falls back to global again.
	require.NoError(t, repo.DeleteSet(ctx, "alice", ScopeWorkspace, "ws-1"))
	resolved, err = resolver.Resolve(ctx, "alice", "ws-1")
	require.NoError(t, err)
	require.Equal(t, SourceGlobal, resolved.Source)

	// Snapshot carries source + seats.
	snapshot := resolved.SnapshotJSON()
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(snapshot, &decoded))
	require.Equal(t, "global", decoded["source"])
}

func TestReplaceSetVersionConflict(t *testing.T) {
	ctx := context.Background()
	db := openRolesDB(t)
	repo := sqlite.NewCouncilRolesRepository(db)

	version, err := repo.ReplaceSet(ctx, "alice", ScopeGlobal, "", 0, validGlobalSet(), "alice")
	require.NoError(t, err)
	require.Equal(t, 1, version)

	// Stale writers conflict, whether behind or ahead.
	_, err = repo.ReplaceSet(ctx, "alice", ScopeGlobal, "", 0, validGlobalSet(), "alice")
	require.ErrorIs(t, err, sqlite.ErrCouncilSetConflict)
	_, err = repo.ReplaceSet(ctx, "alice", ScopeGlobal, "", 3, validGlobalSet(), "alice")
	require.ErrorIs(t, err, sqlite.ErrCouncilSetConflict)

	version, err = repo.ReplaceSet(ctx, "alice", ScopeGlobal, "", 1, validGlobalSet(), "alice")
	require.NoError(t, err)
	require.Equal(t, 2, version)

	// Sets are per account: bob has no set.
	_, _, found, err := repo.LoadSet(ctx, "bob", ScopeGlobal, "")
	require.NoError(t, err)
	require.False(t, found)
}

func TestValidateSetCodes(t *testing.T) {
	profiles := testProfiles()
	codes := func(slots []Slot) map[string]int {
		errs, _ := ValidateSet(profiles, slots)
		out := map[string]int{}
		for _, e := range errs {
			out[e.Code]++
		}
		return out
	}
	base := func() []Slot {
		return []Slot{
			{Role: RoleProposer, Ordinal: 0, ProviderProfileID: "p-openai", Model: "gpt-4o", Enabled: true},
			{Role: RoleJudge, Ordinal: 0, ProviderProfileID: "p-deepseek", Model: "deepseek-chat", Enabled: true},
		}
	}
	require.Empty(t, codes(base()))

	slots := base()
	slots[0].ProviderProfileID = "missing"
	require.Equal(t, 1, codes(slots)["profile_not_found"])

	slots = base()
	slots[0].ProviderProfileID = "p-nokey"
	slots[0].Model = "gpt-4o-mini"
	require.Equal(t, 1, codes(slots)["profile_key_missing"])

	slots = base()
	slots[0].Model = "gpt-unknown"
	require.Equal(t, 1, codes(slots)["model_not_offered"])

	slots = append(base(), Slot{Role: RoleProposer, Ordinal: 0, ProviderProfileID: "p-deepseek", Model: "deepseek-chat", Enabled: true})
	require.Equal(t, 1, codes(slots)["duplicate_slot"])

	slots = base()
	slots[0].Params = json.RawMessage(`{"temperature": 7}`)
	require.Equal(t, 1, codes(slots)["params_out_of_range"])

	slots = base()
	slots[0].Params = json.RawMessage(`{"temperature": 0.5, "max_output_tokens": 4096}`)
	require.Empty(t, codes(slots))

	// Cardinality: no judge primary, and a red-team failover without primary.
	slots = []Slot{
		{Role: RoleProposer, Ordinal: 0, ProviderProfileID: "p-openai", Model: "gpt-4o", Enabled: true},
		{Role: RoleRedTeam, Ordinal: 1, ProviderProfileID: "p-openai", Model: "gpt-4o", Enabled: true},
	}
	got := codes(slots)
	require.Equal(t, 2, got["cardinality"], "judge primary missing + red-team failover without primary")

	// Zero enabled proposers is a cardinality error.
	slots = base()
	slots[0].Enabled = false
	require.Equal(t, 1, codes(slots)["cardinality"])
}
