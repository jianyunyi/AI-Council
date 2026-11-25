package rbac

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// TestMemberRoleUpgradeCorrection executes the shipped operator script
// (docs/operations/upgrade-member-role-permissions.sql) against a simulated
// pre-fix database: a member role still holding the old grants
// task:approve/task:execute, one permission row whose id differs from its
// name (legacy catalogs preserve existing ids), and an unrelated role that
// legitimately holds task:approve and must survive.
func TestMemberRoleUpgradeCorrection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.sqlite")
	db, err := sqlite.Open(path)
	require.NoError(t, err)
	ctx := context.Background()
	s := New(db)
	require.NoError(t, s.SeedPermissions(ctx))

	// Simulate a legacy permission row whose id is NOT its name.
	require.NoError(t, db.Exec("DELETE FROM permission_records WHERE name = 'task:execute'").Error)
	require.NoError(t, db.Create(&sqlite.PermissionRecord{ID: "legacy-execute-id", Name: "task:execute"}).Error)

	// Pre-fix member role: the old eight grants.
	require.NoError(t, db.Create(&sqlite.RoleRecord{ID: "member", Name: "member"}).Error)
	oldGrants := map[string]string{ // permission name -> permission id as stored
		"workspace:read": "workspace:read", "workspace:write": "workspace:write",
		"provider:read": "provider:read", "provider:write": "provider:write",
		"task:read": "task:read", "task:write": "task:write",
		"task:approve": "task:approve", "task:execute": "legacy-execute-id",
	}
	for _, permID := range oldGrants {
		require.NoError(t, db.Create(&sqlite.RolePermissionRecord{RoleID: "member", PermissionID: permID}).Error)
	}

	// An unrelated role that legitimately holds task:approve.
	require.NoError(t, db.Create(&sqlite.RoleRecord{ID: "approvers", Name: "approvers"}).Error)
	require.NoError(t, db.Create(&sqlite.RolePermissionRecord{RoleID: "approvers", PermissionID: "task:approve"}).Error)

	grantNames := func(role string) []string {
		var names []string
		require.NoError(t, db.Raw(
			`SELECT p.name FROM role_permission_records rp
			 JOIN permission_records p ON p.id = rp.permission_id
			 JOIN role_records r ON r.id = rp.role_id
			 WHERE r.name = ? ORDER BY p.name`, role).Scan(&names).Error)
		return names
	}
	require.Contains(t, grantNames("member"), "task:approve")
	require.Contains(t, grantNames("member"), "task:execute")

	script, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "operations", "upgrade-member-role-permissions.sql"))
	require.NoError(t, err)

	// Run the shipped script twice: the correction must be idempotent.
	for run := 0; run < 2; run++ {
		require.NoError(t, db.Exec(string(script)).Error, "script run %d", run)
		require.Equal(t,
			[]string{"provider:read", "provider:write", "task:read", "task:write", "workspace:read", "workspace:write"},
			grantNames("member"), "after script run %d", run)
		require.Equal(t, []string{"task:approve"}, grantNames("approvers"),
			"unrelated roles must keep their grants (run %d)", run)
	}
}
