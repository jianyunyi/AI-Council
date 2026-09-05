package main

import (
	"bytes"
	"context"
	"flag"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/aicouncil/aicouncil/internal/security/rbac"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
)

func parseAuthConfigForTest(t *testing.T, env map[string]string, args ...string) *authConfig {
	t.Helper()
	flags := flag.NewFlagSet("auth", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	config := registerAuthFlags(flags, func(key string) string { return env[key] })
	require.NoError(t, flags.Parse(args))
	return config
}

func TestAuthConfigCookieSecureOnlyExplicitFalseDowngrades(t *testing.T) {
	for _, value := range []string{"", "true", "false", "FALSE", "0", "no", " false ", "typo"} {
		t.Run("value="+value, func(t *testing.T) {
			config := parseAuthConfigForTest(t, map[string]string{"AUTH_COOKIE_SECURE": value}, "--rbac")
			require.NoError(t, config.validate())
			require.Equal(t, value != "false", config.Session.CookieSecure)
			require.Equal(t, "aicouncil_session", config.Session.CookieName)
			require.Equal(t, 8*time.Hour, config.Session.TTL)
		})
	}
}

func TestAuthFlagHelpDoesNotExposeEnvironmentPassword(t *testing.T) {
	flags := flag.NewFlagSet("auth", flag.ContinueOnError)
	var help bytes.Buffer
	flags.SetOutput(&help)
	registerAuthFlags(flags, func(key string) string {
		if key == "COUNCIL_BOOTSTRAP_PASSWORD" {
			return "environment-secret-must-not-appear"
		}
		return ""
	})
	flags.PrintDefaults()
	require.Contains(t, help.String(), "COUNCIL_BOOTSTRAP_PASSWORD")
	require.NotContains(t, help.String(), "environment-secret-must-not-appear")
}

func TestAuthConfigValidatesBootstrapPairsWithoutLeakingSecrets(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		env         map[string]string
		wantEnabled bool
		wantError   bool
	}{
		{name: "disabled"},
		{name: "enabled", args: []string{"--rbac"}, wantEnabled: true},
		{name: "legacy role enables", args: []string{"--rbac-role=operator"}, wantEnabled: true},
		{name: "password pair", args: []string{"--rbac", "--rbac-bootstrap-subject=owner", "--rbac-bootstrap-password=secret-password"}, wantEnabled: true},
		{name: "environment password", args: []string{"--rbac", "--rbac-bootstrap-subject=owner"}, env: map[string]string{"COUNCIL_BOOTSTRAP_PASSWORD": "secret-password"}, wantEnabled: true},
		{name: "legacy token pair", args: []string{"--rbac-role=operator", "--rbac-bootstrap-subject=owner", "--rbac-bootstrap-token=secret-token"}, wantEnabled: true},
		{name: "token with enable flag", args: []string{"--rbac", "--rbac-bootstrap-subject=owner", "--rbac-bootstrap-token=secret-token"}, wantEnabled: true},
		{name: "subject only", args: []string{"--rbac", "--rbac-bootstrap-subject=owner"}, wantError: true},
		{name: "password only", args: []string{"--rbac", "--rbac-bootstrap-password=secret-password"}, wantError: true},
		{name: "token only", args: []string{"--rbac", "--rbac-bootstrap-token=secret-token"}, wantError: true},
		{name: "environment only", args: []string{"--rbac"}, env: map[string]string{"COUNCIL_BOOTSTRAP_PASSWORD": "secret-password"}, wantError: true},
		{name: "both credentials", args: []string{"--rbac", "--rbac-bootstrap-subject=owner", "--rbac-bootstrap-password=secret-password", "--rbac-bootstrap-token=secret-token"}, wantError: true},
		{name: "environment conflicts with token", args: []string{"--rbac", "--rbac-bootstrap-subject=owner", "--rbac-bootstrap-token=secret-token"}, env: map[string]string{"COUNCIL_BOOTSTRAP_PASSWORD": "secret-password"}, wantError: true},
		{name: "bootstrap without enable", args: []string{"--rbac-bootstrap-subject=owner", "--rbac-bootstrap-password=secret-password"}, wantError: true},
		{name: "blank subject", args: []string{"--rbac", "--rbac-bootstrap-subject= ", "--rbac-bootstrap-password=secret-password"}, wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := parseAuthConfigForTest(t, tc.env, tc.args...)
			err := config.validate()
			if tc.wantError {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "secret-password")
				require.NotContains(t, err.Error(), "secret-token")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantEnabled, config.enabled())
		})
	}
	config := parseAuthConfigForTest(t, map[string]string{"COUNCIL_BOOTSTRAP_PASSWORD": "environment-password"}, "--rbac", "--rbac-bootstrap-subject=owner", "--rbac-bootstrap-password=flag-password")
	require.NoError(t, config.validate())
	require.Equal(t, "flag-password", config.BootstrapPassword)
}

func TestConfigureRBACPasswordBootstrapAndRestart(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "auth.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	ctx := context.Background()
	config := parseAuthConfigForTest(t, map[string]string{"COUNCIL_BOOTSTRAP_PASSWORD": "original-password"}, "--rbac", "--rbac-bootstrap-subject=owner")
	service, err := configureRBAC(ctx, db, config)
	require.NoError(t, err)
	token, identity, err := service.Login(ctx, "owner", "original-password", config.Session.TTL)
	require.NoError(t, err)
	require.WithinDuration(t, time.Now().Add(8*time.Hour), *identity.ExpiresAt, 5*time.Second)
	for _, permission := range []string{"workspace:read", "workspace:write", "task:read", "task:write", "task:approve", "task:execute", "admin:users", "admin:roles", "admin:permissions"} {
		require.NoError(t, service.AuthorizePermission(ctx, token, permission), permission)
	}
	config.BootstrapPassword = "ignored-password"
	_, err = configureRBAC(ctx, db, config)
	require.NoError(t, err)
	_, _, err = service.Login(ctx, "owner", "original-password", time.Hour)
	require.NoError(t, err)
	_, _, err = service.Login(ctx, "owner", "ignored-password", time.Hour)
	require.ErrorIs(t, err, rbac.ErrUnauthorized)
	_, err = service.ReplaceRolePermissions(ctx, "admin", []string{"admin:users"})
	require.NoError(t, err)
	restart := parseAuthConfigForTest(t, nil, "--rbac")
	service, err = configureRBAC(ctx, db, restart)
	require.NoError(t, err)
	roles, err := service.ListRoles(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"admin:users"}, roles[0].Permissions)
	permissions, err := service.ListPermissions(ctx)
	require.NoError(t, err)
	require.Len(t, permissions, 10)
}

func TestConfigureRBACLegacyBootstrapKeepsBearerToken(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "legacy.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	ctx := context.Background()
	config := parseAuthConfigForTest(t, nil, "--rbac-role=operator", "--rbac-bootstrap-subject=owner", "--rbac-bootstrap-token=original-token")
	service, err := configureRBAC(ctx, db, config)
	require.NoError(t, err)
	require.NoError(t, service.Authorize(ctx, "original-token", "operator"))
	require.NoError(t, service.AuthorizePermission(ctx, "original-token", "task:execute"))
	config.BootstrapToken = "ignored-token"
	_, err = configureRBAC(ctx, db, config)
	require.NoError(t, err)
	_, err = service.Authenticate(ctx, "original-token")
	require.NoError(t, err)
	_, err = service.Authenticate(ctx, "ignored-token")
	require.ErrorIs(t, err, rbac.ErrUnauthorized)
}

func TestConfigureRBACDisabledDoesNotSeedOrOpenDatabase(t *testing.T) {
	service, err := configureRBAC(context.Background(), nil, parseAuthConfigForTest(t, nil))
	require.NoError(t, err)
	require.Nil(t, service)
}
