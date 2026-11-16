package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"strings"
	"time"

	oauthsvc "github.com/aicouncil/aicouncil/internal/security/oauth"
	"github.com/aicouncil/aicouncil/internal/security/rbac"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	transport "github.com/aicouncil/aicouncil/internal/transport/council"
	"gorm.io/gorm"
)

type authConfig struct {
	Enabled           bool
	Role              string
	BootstrapSubject  string
	BootstrapPassword string
	BootstrapToken    string
	Session           transport.SessionOptions
	passwordFromEnv   string
}

func migrateLegacyOwnership(ctx context.Context, db *gorm.DB, bootstrapSubject string) error {
	models := []any{&sqlite.WorkspaceRecord{}, &sqlite.TaskRecord{}, &sqlite.ArtifactRecord{}, &sqlite.ProviderProfileRecord{}}
	needsOwner := false
	for _, model := range models {
		var count int64
		if err := db.WithContext(ctx).Model(model).Where("owner_user_id = '' OR owner_user_id IS NULL").Count(&count).Error; err != nil {
			return err
		}
		needsOwner = needsOwner || count > 0
	}
	if !needsOwner {
		return nil
	}
	if strings.TrimSpace(bootstrapSubject) == "" {
		return errors.New("existing unowned resources require a bootstrap administrator for ownership migration")
	}
	var admin sqlite.UserRecord
	if err := db.WithContext(ctx).Where("subject = ?", bootstrapSubject).First(&admin).Error; err != nil {
		return fmt.Errorf("load bootstrap administrator for ownership migration: %w", err)
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, model := range models {
			if err := tx.Model(model).Where("owner_user_id = '' OR owner_user_id IS NULL").Update("owner_user_id", admin.ID).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func configuredOAuth(db *gorm.DB, getenv func(string) string) (*oauthsvc.Service, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(getenv("PUBLIC_BASE_URL")), "/")
	githubID, githubSecret := getenv("GITHUB_OAUTH_CLIENT_ID"), getenv("GITHUB_OAUTH_CLIENT_SECRET")
	googleID, googleSecret := getenv("GOOGLE_OAUTH_CLIENT_ID"), getenv("GOOGLE_OAUTH_CLIENT_SECRET")
	if githubID == "" && githubSecret == "" && googleID == "" && googleSecret == "" && baseURL == "" {
		return nil, nil
	}
	if baseURL == "" || ((githubID == "") != (githubSecret == "")) || ((googleID == "") != (googleSecret == "")) || (githubID == "" && googleID == "") {
		return nil, errors.New("configure PUBLIC_BASE_URL and complete client ID/secret pairs for at least one OAuth provider")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("PUBLIC_BASE_URL must be an HTTPS origin or base path without credentials, query, or fragment")
	}
	configs := make(map[string]oauthsvc.Config)
	if githubID != "" {
		configs["github"] = oauthsvc.Config{ClientID: githubID, ClientSecret: githubSecret, RedirectURL: baseURL + "/api/v1/auth/oauth/github/callback", AuthURL: "https://github.com/login/oauth/authorize", TokenURL: "https://github.com/login/oauth/access_token", APIBaseURL: "https://api.github.com"}
	}
	if googleID != "" {
		configs["google"] = oauthsvc.Config{ClientID: googleID, ClientSecret: googleSecret, RedirectURL: baseURL + "/api/v1/auth/oauth/google/callback", AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", JWKSURL: "https://www.googleapis.com/oauth2/v3/certs"}
	}
	return oauthsvc.New(db, configs, nil), nil
}

func registerAuthFlags(flags *flag.FlagSet, getenv func(string) string) *authConfig {
	config := &authConfig{
		passwordFromEnv: getenv("COUNCIL_BOOTSTRAP_PASSWORD"),
		Session: transport.SessionOptions{
			CookieName: "aicouncil_session", CookieSecure: getenv("AUTH_COOKIE_SECURE") != "false", TTL: 8 * time.Hour,
		},
	}
	flags.BoolVar(&config.Enabled, "rbac", false, "Enable SQLite users, sessions and permission-based access")
	flags.StringVar(&config.Role, "rbac-role", "", "Compatibility RBAC enable flag and legacy bootstrap role; routes use permissions")
	flags.StringVar(&config.BootstrapSubject, "rbac-bootstrap-subject", "", "Optional one-time RBAC bootstrap user subject")
	// Resolve the environment fallback after parsing so --help never prints it.
	flags.StringVar(&config.BootstrapPassword, "rbac-bootstrap-password", "", "Bootstrap administrator password (prefer COUNCIL_BOOTSTRAP_PASSWORD)")
	flags.StringVar(&config.BootstrapToken, "rbac-bootstrap-token", "", "Legacy bootstrap user bearer token")
	return config
}

func (config *authConfig) enabled() bool { return config.Enabled || config.Role != "" }

func (config *authConfig) validate() error {
	if config.BootstrapPassword == "" {
		config.BootstrapPassword = config.passwordFromEnv
	}
	if config.Role != "" && strings.TrimSpace(config.Role) == "" {
		return errors.New("rbac-role must not be blank")
	}
	subject := config.BootstrapSubject != ""
	password := config.BootstrapPassword != ""
	token := config.BootstrapToken != ""
	if !subject && !password && !token {
		return nil
	}
	if !config.enabled() {
		return errors.New("RBAC bootstrap requires --rbac or --rbac-role")
	}
	if password && token {
		return errors.New("RBAC bootstrap password and token are mutually exclusive")
	}
	if !subject || strings.TrimSpace(config.BootstrapSubject) == "" || (!password && !token) {
		return errors.New("RBAC bootstrap requires a subject and exactly one password or token")
	}
	return nil
}

func configureRBAC(ctx context.Context, db *gorm.DB, config *authConfig) (*rbac.Service, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if !config.enabled() {
		return nil, nil
	}
	service := rbac.New(db)
	if err := service.SeedPermissions(ctx); err != nil {
		return nil, err
	}
	if config.BootstrapPassword != "" {
		// Keep BootstrapAdmin's existing API, but never print its temporary token.
		if _, err := service.BootstrapAdmin(ctx, config.BootstrapSubject, config.BootstrapPassword, config.Session.TTL); err != nil {
			return nil, err
		}
	} else if config.BootstrapToken != "" {
		role := config.Role
		if role == "" {
			role = "admin"
		}
		if err := service.BootstrapLegacyAdmin(ctx, config.BootstrapSubject, config.BootstrapToken, role); err != nil {
			return nil, err
		}
	}
	return service, nil
}
