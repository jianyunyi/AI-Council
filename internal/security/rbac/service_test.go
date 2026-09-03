package rbac

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRBACAuthorizeByRole(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(db)
	ctx := context.Background()
	require.NoError(t, s.CreateUser(ctx, "u1", "secret"))
	require.NoError(t, s.GrantRole(ctx, "u1", "operator"))
	require.NoError(t, s.Authorize(ctx, "secret", "operator"))
	require.ErrorIs(t, s.Authorize(ctx, "secret", "admin"), ErrForbidden)
	require.ErrorIs(t, s.Authorize(ctx, "bad", "operator"), ErrUnauthorized)
}

func TestPasswordHashAndVerification(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	require.NoError(t, err)
	require.NotContains(t, hash, "correct horse battery staple")
	require.NoError(t, VerifyPassword(hash, "correct horse battery staple"))
	require.Error(t, VerifyPassword(hash, "wrong password"))
	require.Error(t, VerifyPassword("not-an-argon2id-hash", "correct horse battery staple"))
}

func TestRBACPermissionAndTokenLifecycle(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(db)
	ctx := context.Background()

	require.NoError(t, s.CreateUserWithPassword(ctx, "alice", "password"))
	require.NoError(t, s.CreateRole(ctx, "operator"))
	require.NoError(t, s.GrantPermission(ctx, "operator", "task:read"))
	require.NoError(t, s.AssignRole(ctx, "alice", "operator"))

	plain, record, err := s.IssueToken(ctx, "alice", time.Hour)
	require.NoError(t, err)
	require.NotEmpty(t, plain)
	require.NotEmpty(t, record.TokenHash)
	require.NotContains(t, record.TokenHash, plain)

	identity, err := s.Authenticate(ctx, plain)
	require.NoError(t, err)
	require.Equal(t, "alice", identity.Subject)
	require.Contains(t, identity.Roles, "operator")
	require.Contains(t, identity.Permissions, "task:read")
	require.NoError(t, s.AuthorizePermission(ctx, plain, "task:read"))
	require.ErrorIs(t, s.AuthorizePermission(ctx, plain, "task:write"), ErrForbidden)

	require.NoError(t, s.RevokeToken(ctx, plain))
	_, err = s.Authenticate(ctx, plain)
	require.ErrorIs(t, err, ErrUnauthorized)
}

func TestTokenRejectsDisabledAndExpiredUsers(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(db)
	ctx := context.Background()

	require.NoError(t, s.CreateUserWithPassword(ctx, "alice", "password"))
	expired, _, err := s.IssueToken(ctx, "alice", -time.Second)
	require.NoError(t, err)
	_, err = s.Authenticate(ctx, expired)
	require.ErrorIs(t, err, ErrUnauthorized)

	active, _, err := s.IssueToken(ctx, "alice", time.Hour)
	require.NoError(t, err)
	require.NoError(t, db.Model(&sqlite.UserRecord{}).Where("subject = ?", "alice").Update("disabled", true).Error)
	_, err = s.Authenticate(ctx, active)
	require.ErrorIs(t, err, ErrUnauthorized)
}

func TestBootstrapAdminIsIdempotent(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(db)
	ctx := context.Background()

	token, err := s.BootstrapAdmin(ctx, "admin", "password", time.Hour)
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.NoError(t, s.AuthorizePermission(ctx, token, "admin:*"))

	token, err = s.BootstrapAdmin(ctx, "admin", "different", time.Hour)
	require.NoError(t, err)
	require.Empty(t, token)

	var users int64
	require.NoError(t, db.Model(&sqlite.UserRecord{}).Where("subject = ?", "admin").Count(&users).Error)
	require.EqualValues(t, 1, users)
}

func TestBootstrapAdminRepairsExistingUsers(t *testing.T) {
	tests := []struct {
		name             string
		passwordHash     *string
		disabled         bool
		wantPassword     string
		preservePassword bool
	}{
		{name: "ordinary", passwordHash: passwordHashForTest(t, "old"), wantPassword: "old", preservePassword: true},
		{name: "missing password", wantPassword: "bootstrap"},
		{name: "disabled", passwordHash: passwordHashForTest(t, "old"), disabled: true, wantPassword: "old", preservePassword: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
			require.NoError(t, err)
			sqlDB, _ := db.DB()
			t.Cleanup(func() { _ = sqlDB.Close() })
			require.NoError(t, db.Create(&sqlite.UserRecord{
				ID: "admin-user", Subject: "admin-user", PasswordHash: tt.passwordHash, Disabled: tt.disabled,
			}).Error)

			s := New(db)
			token, err := s.BootstrapAdmin(context.Background(), "admin-user", "bootstrap", time.Hour)
			require.NoError(t, err)
			require.Empty(t, token)

			var user sqlite.UserRecord
			require.NoError(t, db.Where("subject = ?", "admin-user").First(&user).Error)
			require.False(t, user.Disabled)
			require.NotNil(t, user.PasswordHash)
			require.NoError(t, VerifyPassword(*user.PasswordHash, tt.wantPassword))
			if tt.preservePassword {
				require.ErrorIs(t, VerifyPassword(*user.PasswordHash, "bootstrap"), ErrInvalidPassword)
			}

			issued, _, err := s.IssueToken(context.Background(), user.ID, time.Hour)
			require.NoError(t, err)
			require.NoError(t, s.AuthorizePermission(context.Background(), issued, "admin:*"))
		})
	}
}

func TestBootstrapAdminRollsBackRepairsOnFailure(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.Create(&sqlite.UserRecord{ID: "admin-user", Subject: "admin-user", Disabled: true}).Error)
	require.NoError(t, db.Create(&sqlite.PermissionRecord{ID: "admin:*", Name: "conflicting"}).Error)

	_, err = New(db).BootstrapAdmin(context.Background(), "admin-user", "bootstrap", time.Hour)
	require.Error(t, err)

	var user sqlite.UserRecord
	require.NoError(t, db.Where("subject = ?", "admin-user").First(&user).Error)
	require.True(t, user.Disabled)
	require.Nil(t, user.PasswordHash)
}

func TestBootstrapAdminIsConcurrentSafe(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(2)
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(db)
	ctx := context.Background()

	start := make(chan struct{})
	var wg sync.WaitGroup
	var tokens atomic.Int32
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			token, err := s.BootstrapAdmin(ctx, "admin", "password", time.Hour)
			if token != "" {
				tokens.Add(1)
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, tokens.Load())

	var users, links int64
	require.NoError(t, db.Model(&sqlite.UserRecord{}).Where("subject = ?", "admin").Count(&users).Error)
	require.NoError(t, db.Model(&sqlite.UserRoleRecord{}).Count(&links).Error)
	require.EqualValues(t, 1, users)
	require.EqualValues(t, 1, links)
}

func TestDatabaseErrorsAreNotUnauthorized(t *testing.T) {
	t.Run("issue token", func(t *testing.T) {
		db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
		require.NoError(t, err)
		sqlDB, _ := db.DB()
		t.Cleanup(func() { _ = sqlDB.Close() })
		require.NoError(t, db.Migrator().DropTable(&sqlite.UserRecord{}))

		_, _, err = New(db).IssueToken(context.Background(), "alice", time.Hour)
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrUnauthorized)
		require.ErrorContains(t, err, "load token user")
	})

	t.Run("authenticate legacy user", func(t *testing.T) {
		db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
		require.NoError(t, err)
		sqlDB, _ := db.DB()
		t.Cleanup(func() { _ = sqlDB.Close() })
		require.NoError(t, db.Migrator().DropTable(&sqlite.UserRecord{}))

		_, err = New(db).Authenticate(context.Background(), "token")
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrUnauthorized)
		require.ErrorContains(t, err, "load legacy user")
	})

	t.Run("authenticate token user", func(t *testing.T) {
		db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
		require.NoError(t, err)
		sqlDB, _ := db.DB()
		t.Cleanup(func() { _ = sqlDB.Close() })
		s := New(db)
		require.NoError(t, s.CreateUserWithPassword(context.Background(), "alice", "password"))
		token, _, err := s.IssueToken(context.Background(), "alice", time.Hour)
		require.NoError(t, err)
		require.NoError(t, db.Exec("PRAGMA foreign_keys = OFF").Error)
		require.NoError(t, db.Migrator().DropTable(&sqlite.UserRecord{}))

		_, err = s.Authenticate(context.Background(), token)
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrUnauthorized)
		require.ErrorContains(t, err, "load identity user")
	})
}

func passwordHashForTest(t *testing.T, password string) *string {
	t.Helper()
	hash, err := HashPassword(password)
	require.NoError(t, err)
	return &hash
}

func TestSetPassword(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(db)
	ctx := context.Background()

	require.NoError(t, s.CreateUserWithPassword(ctx, "alice", "old"))
	require.NoError(t, s.SetPassword(ctx, "alice", "new"))
	var user sqlite.UserRecord
	require.NoError(t, db.Where("subject = ?", "alice").First(&user).Error)
	require.NotNil(t, user.PasswordHash)
	require.NoError(t, VerifyPassword(*user.PasswordHash, "new"))
	require.Error(t, VerifyPassword(*user.PasswordHash, "old"))
	require.Error(t, s.SetPassword(ctx, "missing", "new"))
}

func TestDuplicateRBACLinksAreRejected(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(db)
	ctx := context.Background()

	require.NoError(t, s.CreateUserWithPassword(ctx, "alice", "password"))
	require.Error(t, s.CreateUserWithPassword(ctx, "alice", "password"))
	require.NoError(t, s.CreateRole(ctx, "operator"))
	require.NoError(t, s.GrantPermission(ctx, "operator", "task:read"))
	require.Error(t, s.GrantPermission(ctx, "operator", "task:read"))
	require.NoError(t, s.AssignRole(ctx, "alice", "operator"))
	require.Error(t, s.AssignRole(ctx, "alice", "operator"))
}

func TestAuthenticateUnknownToken(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	_, err = New(db).Authenticate(context.Background(), "unknown")
	require.True(t, errors.Is(err, ErrUnauthorized))
}

func TestLoginWithPasswordReturnsTokenAndIdentity(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(db)
	ctx := context.Background()
	require.NoError(t, s.CreateUserWithPassword(ctx, "alice", "password"))

	token, identity, err := s.Login(ctx, "alice", "password", time.Hour)
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.Equal(t, "alice", identity.Subject)

	_, _, err = s.Login(ctx, "alice", "wrong", time.Hour)
	require.ErrorIs(t, err, ErrUnauthorized)
}

func TestAuthorizePermissionSupportsNamespaceWildcards(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(db)
	ctx := context.Background()
	require.NoError(t, s.CreateUserWithPassword(ctx, "alice", "password"))
	require.NoError(t, s.CreateRole(ctx, "admin"))
	require.NoError(t, s.GrantPermission(ctx, "admin", "admin:*"))
	require.NoError(t, s.AssignRole(ctx, "alice", "admin"))
	token, _, err := s.IssueToken(ctx, "alice", time.Hour)
	require.NoError(t, err)
	require.NoError(t, s.AuthorizePermission(ctx, token, "admin:users"))
	require.ErrorIs(t, s.AuthorizePermission(ctx, token, "billing:users"), ErrForbidden)
}

func TestManagedProjectionsAndAtomicRoleValidation(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(db)
	ctx := context.Background()
	require.NoError(t, s.CreateRole(ctx, "reader"))
	require.NoError(t, s.CreateRole(ctx, "writer"))
	managed, err := s.CreateManagedUser(ctx, "alice", "password", []string{"reader"})
	require.NoError(t, err)
	require.Equal(t, User{Subject: "alice", Roles: []string{"reader"}}, managed)
	require.Equal(t, User{Subject: "alice", Roles: []string{"reader"}}, managed)
	_, err = s.UpdateUser(ctx, "alice", "", []string{"missing"}, true)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	updated, err := s.UpdateUser(ctx, "alice", "", []string{"reader"}, true)
	require.NoError(t, err)
	require.True(t, updated.Disabled)
	users, err := s.ListUsers(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"reader"}, users[0].Roles)
	roles, err := s.ListRoles(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, roles)
}

func TestLoginUsesExactUserIdentityWhenIDAndSubjectCollide(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	hash, err := HashPassword("password")
	require.NoError(t, err)
	require.NoError(t, db.Create(&sqlite.UserRecord{ID: "a", Subject: "z"}).Error)
	require.NoError(t, db.Create(&sqlite.UserRecord{ID: "z", Subject: "attacker", PasswordHash: &hash}).Error)
	token, _, err := New(db).Login(context.Background(), "attacker", "password", time.Hour)
	require.NoError(t, err)
	identity, err := New(db).Authenticate(context.Background(), token)
	require.NoError(t, err)
	require.Equal(t, "attacker", identity.Subject)
}

func TestManagedRolePermissionReplacementFindsExistingPermissionByName(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	ctx := context.Background()
	require.NoError(t, db.Create(&sqlite.PermissionRecord{ID: "p1", Name: "task:read"}).Error)
	s := New(db)
	role, err := s.CreateManagedRole(ctx, "reader", []string{"task:read"})
	require.NoError(t, err)
	require.Equal(t, []string{"task:read"}, role.Permissions)
	role, err = s.ReplaceRolePermissions(ctx, "reader", []string{"task:read"})
	require.NoError(t, err)
	require.Equal(t, []string{"task:read"}, role.Permissions)
}
