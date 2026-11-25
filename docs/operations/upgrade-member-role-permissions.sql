-- Upgrade correction: strip task:approve / task:execute from the built-in
-- "member" role in databases created before the separation-of-duties fix.
--
-- Background: self-service registration used to grant the member role
-- task:approve and task:execute, so a freshly registered user could draft,
-- approve and execute their own tasks. The code fix (AssignDefaultMemberRole)
-- only changes what NEW registrations receive; it never removes grants that
-- an existing database already stored. Run this script ONCE against the
-- Council database of any deployment whose data predates the fix.
--
-- Properties:
--   * Table/column names verified against the GORM models in
--     internal/storage/sqlite (role_records, permission_records,
--     role_permission_records). Do not "simplify" them to roles/permissions:
--     those tables do not exist.
--   * The DELETE joins on permission NAME, not id: a permission row's id
--     normally equals its name, but pre-existing rows may carry a different
--     id (the catalog seeder deliberately preserves existing ids).
--   * Idempotent: running it again deletes nothing.
--   * Other roles are untouched, even if they legitimately hold
--     task:approve / task:execute.
--   * Caveat: if an administrator deliberately granted task:approve or
--     task:execute to the member role AFTER the code fix, this script
--     removes that grant too. Re-grant it afterwards if it was intentional.
--   * Back up the database first (deploy/backup.ps1 or aicouncil-admin
--     backup).
--
-- Usage:  sqlite3 <council.db> < upgrade-member-role-permissions.sql
-- (Regression-tested by TestMemberRoleUpgradeCorrection in
-- internal/security/rbac, which executes this exact file.)

DELETE FROM role_permission_records
WHERE role_id IN (SELECT id FROM role_records WHERE name = 'member')
  AND permission_id IN (
    SELECT id FROM permission_records
    WHERE name IN ('task:approve', 'task:execute')
  );

-- Verification (optional, run manually afterwards): the member role's
-- remaining grants. Expected after correction:
--   provider:read, provider:write, task:read, task:write,
--   workspace:read, workspace:write
-- (group:create is added by the current code on the next registration and
-- is harmless.) task:approve / task:execute must NOT appear.
--
-- SELECT p.name
-- FROM role_permission_records rp
-- JOIN permission_records p ON p.id = rp.permission_id
-- JOIN role_records r ON r.id = rp.role_id
-- WHERE r.name = 'member'
-- ORDER BY p.name;
