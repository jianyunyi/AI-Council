package main

import (
	"context"
	"errors"
	"flag"
	"strings"
	"time"

	"github.com/aicouncil/aicouncil/internal/security/rbac"
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
