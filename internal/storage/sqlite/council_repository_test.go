package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCouncilRepositoryRoundTripsMetadataWithoutSecrets(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "council.db"))
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	repo := NewCouncilRepository(db)
	ctx := context.Background()
	require.NoError(t, repo.SaveProviderProfile(ctx, ProviderProfileRecord{ID: "profile-1", Provider: "openai", Model: "gpt-test", ParametersJSON: []byte(`{"temperature":0}`)}))
	require.NoError(t, repo.SaveWorkspace(ctx, WorkspaceRecord{ID: "workspace-1", CanonicalRoot: `C:\workspace`, RunnerID: "runner-1"}))
	require.NoError(t, repo.SaveTask(ctx, TaskRecord{ID: "task-1", WorkspaceID: "workspace-1", Requirement: "build"}))
	require.NoError(t, repo.SaveSeats(ctx, []SeatRecord{{ID: "seat-1", RunID: "run-1", ProviderProfileID: "profile-1", Role: "proposer", ProposalAlias: "Proposal A"}}))
	require.NoError(t, repo.RecordInvocation(ctx, ModelInvocationRecord{ID: "inv-1", RunID: "run-1", SeatID: "seat-1", Stage: "propose", ProviderRequestID: "req-1", InputTokens: 2, OutputTokens: 3, ErrorCode: ""}))

	var got ProviderProfileRecord
	require.NoError(t, db.First(&got, "id = ?", "profile-1").Error)
	require.NotContains(t, string(got.ParametersJSON), "sk-test-secret")
	var invocation ModelInvocationRecord
	require.NoError(t, db.First(&invocation, "id = ?", "inv-1").Error)
	require.Equal(t, int64(2), invocation.InputTokens)
	require.Equal(t, int64(3), invocation.OutputTokens)
}
