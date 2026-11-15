package oauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/aicouncil/aicouncil/internal/security/identity"
	"github.com/aicouncil/aicouncil/internal/security/rbac"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"gorm.io/gorm"
)

var ErrInvalidFlow = errors.New("invalid or expired OAuth flow")
var ErrProviderUnavailable = errors.New("OAuth provider unavailable")

type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	AuthURL      string
	TokenURL     string
	APIBaseURL   string
	JWKSURL      string
}

type Flow struct {
	URL      string
	State    string
	Verifier string
	Nonce    string
}

type Result struct {
	AccessToken string
	Identity    rbac.Identity
}

type Service struct {
	db      *gorm.DB
	configs map[string]Config
	client  *http.Client
	now     func() time.Time
}

func New(db *gorm.DB, configs map[string]Config, client *http.Client) *Service {
	copyConfigs := make(map[string]Config, len(configs))
	for name, config := range configs {
		copyConfigs[strings.ToLower(name)] = config
	}
	if client == nil {
		client = &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &Service{db: db, configs: copyConfigs, client: client, now: time.Now}
}

func (s *Service) Enabled(provider string) bool {
	config, ok := s.configs[strings.ToLower(provider)]
	return ok && config.ClientID != "" && config.ClientSecret != "" && config.RedirectURL != ""
}

func (s *Service) Start(ctx context.Context, provider string) (Flow, error) {
	provider = strings.ToLower(provider)
	config, ok := s.configs[provider]
	if !ok || !s.Enabled(provider) {
		return Flow{}, ErrProviderUnavailable
	}
	if config.AuthURL == "" || config.TokenURL == "" {
		return Flow{}, ErrProviderUnavailable
	}
	redirect, err := url.Parse(config.RedirectURL)
	if err != nil || (redirect.Scheme != "https" && redirect.Hostname() != "localhost") {
		return Flow{}, ErrProviderUnavailable
	}
	state, err := randomURLToken(32)
	if err != nil {
		return Flow{}, err
	}
	verifier, err := randomURLToken(32)
	if err != nil {
		return Flow{}, err
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		return Flow{}, err
	}
	now := s.now()
	flow := sqlite.OAuthFlowRecord{
		StateHash: hash(state), Provider: provider, NonceHash: hash(nonce), VerifierHash: hash(verifier),
		ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now,
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("expires_at <= ? OR consumed_at IS NOT NULL", now).Delete(&sqlite.OAuthFlowRecord{}).Error; err != nil {
			return err
		}
		return tx.Create(&flow).Error
	}); err != nil {
		return Flow{}, fmt.Errorf("persist OAuth flow: %w", err)
	}
	challenge := base64.RawURLEncoding.EncodeToString(sumBytes(verifier))
	values := url.Values{
		"client_id": {config.ClientID}, "redirect_uri": {config.RedirectURL}, "response_type": {"code"},
		"state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	if provider == "github" {
		values.Set("scope", "read:user user:email")
	} else {
		values.Set("scope", "openid email profile")
		values.Set("nonce", nonce)
	}
	authURL, err := url.Parse(config.AuthURL)
	if err != nil || authURL.Scheme != "https" {
		return Flow{}, ErrProviderUnavailable
	}
	authURL.RawQuery = values.Encode()
	return Flow{URL: authURL.String(), State: state, Verifier: verifier, Nonce: nonce}, nil
}

func (s *Service) Complete(ctx context.Context, provider, code, state, verifier, nonce string, ttl time.Duration) (Result, error) {
	provider = strings.ToLower(provider)
	if !s.Enabled(provider) || code == "" || state == "" || verifier == "" || nonce == "" {
		return Result{}, ErrInvalidFlow
	}
	config := s.configs[provider]
	now := s.now()
	var flow sqlite.OAuthFlowRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("state_hash = ? AND provider = ? AND consumed_at IS NULL AND expires_at > ?", hash(state), provider, now).Limit(1).Find(&flow)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 || flow.VerifierHash != hash(verifier) || flow.NonceHash != hash(nonce) {
			return ErrInvalidFlow
		}
		result = tx.Model(&sqlite.OAuthFlowRecord{}).Where("state_hash = ? AND consumed_at IS NULL", flow.StateHash).Update("consumed_at", now)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInvalidFlow
		}
		return nil
	})
	if err != nil {
		return Result{}, ErrInvalidFlow
	}
	accessToken, idToken, err := s.exchange(ctx, provider, config, code, verifier)
	if err != nil {
		return Result{}, err
	}
	var providerSubject, email string
	if provider == "github" {
		providerSubject, email, err = s.githubIdentity(ctx, config, accessToken)
	} else {
		providerSubject, email, err = s.googleIdentity(ctx, config, idToken, nonce)
	}
	if err != nil {
		return Result{}, err
	}
	userID, err := s.linkIdentity(ctx, provider, providerSubject, email)
	if err != nil {
		return Result{}, err
	}
	if ttl <= 0 {
		ttl = 8 * time.Hour
	}
	auth := rbac.New(s.db)
	plain, _, err := auth.IssueToken(ctx, userID, ttl)
	if err != nil {
		return Result{}, err
	}
	identity, err := auth.Authenticate(ctx, plain)
	if err != nil {
		return Result{}, err
	}
	return Result{AccessToken: plain, Identity: identity}, nil
}

func (s *Service) exchange(ctx context.Context, provider string, config Config, code, verifier string) (string, string, error) {
	form := url.Values{"client_id": {config.ClientID}, "client_secret": {config.ClientSecret}, "code": {code}, "redirect_uri": {config.RedirectURL}, "code_verifier": {verifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", ErrProviderUnavailable
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", "", ErrProviderUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", ErrProviderUnavailable
	}
	var token struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&token); err != nil || token.AccessToken == "" || (provider == "google" && token.IDToken == "") {
		return "", "", ErrProviderUnavailable
	}
	return token.AccessToken, token.IDToken, nil
}

func (s *Service) githubIdentity(ctx context.Context, config Config, accessToken string) (string, string, error) {
	base := config.APIBaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	var user struct {
		ID json.RawMessage `json:"id"`
	}
	if err := s.getJSON(ctx, strings.TrimRight(base, "/")+"/user", accessToken, &user); err != nil {
		return "", "", err
	}
	subject := strings.Trim(string(user.ID), `"`)
	if subject == "" || subject == "null" {
		return "", "", ErrProviderUnavailable
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := s.getJSON(ctx, strings.TrimRight(base, "/")+"/user/emails", accessToken, &emails); err != nil {
		return "", "", err
	}
	selected := ""
	for _, item := range emails {
		if item.Verified && item.Primary {
			selected = item.Email
			break
		}
	}
	if selected == "" {
		for _, item := range emails {
			if item.Verified {
				selected = item.Email
				break
			}
		}
	}
	return normalizeVerifiedEmail(subject, selected)
}

func (s *Service) googleIdentity(ctx context.Context, config Config, idToken, nonce string) (string, string, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return "", "", ErrProviderUnavailable
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}
	if decodeJWTPart(parts[0], &header) != nil || header.Algorithm != "RS256" || header.KeyID == "" {
		return "", "", ErrProviderUnavailable
	}
	jwksURL := config.JWKSURL
	if jwksURL == "" {
		jwksURL = "https://www.googleapis.com/oauth2/v3/certs"
	}
	var keySet struct {
		Keys []struct {
			KeyID     string `json:"kid"`
			KeyType   string `json:"kty"`
			Use       string `json:"use"`
			Algorithm string `json:"alg"`
			Modulus   string `json:"n"`
			Exponent  string `json:"e"`
		} `json:"keys"`
	}
	if err := s.getJSON(ctx, jwksURL, "", &keySet); err != nil {
		return "", "", err
	}
	var publicKey *rsa.PublicKey
	for _, key := range keySet.Keys {
		if key.KeyID != header.KeyID || key.KeyType != "RSA" || (key.Algorithm != "" && key.Algorithm != "RS256") {
			continue
		}
		modulus, errN := base64.RawURLEncoding.DecodeString(key.Modulus)
		exponent, errE := base64.RawURLEncoding.DecodeString(key.Exponent)
		if errN != nil || errE != nil || len(modulus) == 0 || len(exponent) == 0 || len(exponent) > 4 {
			continue
		}
		exp := 0
		for _, b := range exponent {
			exp = exp<<8 | int(b)
		}
		if exp < 3 {
			continue
		}
		n := new(big.Int).SetBytes(modulus)
		if n.BitLen() < 2048 {
			continue
		}
		publicKey = &rsa.PublicKey{N: n, E: exp}
		break
	}
	if publicKey == nil {
		return "", "", ErrProviderUnavailable
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", "", ErrProviderUnavailable
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], sig); err != nil {
		return "", "", ErrProviderUnavailable
	}
	var claims struct {
		Issuer          string          `json:"iss"`
		Subject         string          `json:"sub"`
		Audience        json.RawMessage `json:"aud"`
		Email           string          `json:"email"`
		EmailVerified   bool            `json:"email_verified"`
		Nonce           string          `json:"nonce"`
		AuthorizedParty string          `json:"azp"`
		Expires         int64           `json:"exp"`
		IssuedAt        int64           `json:"iat"`
	}
	if decodeJWTPart(parts[1], &claims) != nil {
		return "", "", ErrProviderUnavailable
	}
	if claims.Issuer != "https://accounts.google.com" && claims.Issuer != "accounts.google.com" {
		return "", "", ErrProviderUnavailable
	}
	currentTime := s.now()
	if claims.Subject == "" || claims.Nonce != nonce || !claims.EmailVerified || claims.IssuedAt <= 0 || claims.Expires <= currentTime.Unix() || claims.Expires <= claims.IssuedAt || claims.IssuedAt < currentTime.Add(-24*time.Hour).Unix() || claims.IssuedAt > currentTime.Add(time.Minute).Unix() || (claims.AuthorizedParty != "" && claims.AuthorizedParty != config.ClientID) {
		return "", "", ErrProviderUnavailable
	}
	var audience string
	if json.Unmarshal(claims.Audience, &audience) == nil {
		if audience != config.ClientID {
			return "", "", ErrProviderUnavailable
		}
	} else {
		var audiences []string
		if json.Unmarshal(claims.Audience, &audiences) != nil {
			return "", "", ErrProviderUnavailable
		}
		found := false
		for _, value := range audiences {
			if value == config.ClientID {
				found = true
			}
		}
		if !found {
			return "", "", ErrProviderUnavailable
		}
		if claims.AuthorizedParty != config.ClientID {
			return "", "", ErrProviderUnavailable
		}
	}
	return normalizeVerifiedEmail(claims.Subject, claims.Email)
}

func (s *Service) getJSON(ctx context.Context, endpoint, accessToken string, target any) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" {
		return ErrProviderUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ErrProviderUnavailable
	}
	req.Header.Set("Accept", "application/json")
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	if strings.EqualFold(parsed.Hostname(), "api.github.com") {
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "AI-Council")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return ErrProviderUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrProviderUnavailable
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(target); err != nil {
		return ErrProviderUnavailable
	}
	return nil
}

func (s *Service) linkIdentity(ctx context.Context, provider, providerSubject, email string) (string, error) {
	if providerSubject == "" || email == "" {
		return "", ErrProviderUnavailable
	}
	if err := rbac.New(s.db).SeedPermissions(ctx); err != nil {
		return "", err
	}
	var userID string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var linked sqlite.OAuthIdentityRecord
		if err := tx.Where("provider = ? AND provider_subject = ?", provider, providerSubject).Limit(1).Find(&linked).Error; err != nil {
			return err
		}
		if linked.ID != "" {
			userID = linked.UserID
			return nil
		}
		var user sqlite.UserRecord
		if err := tx.Where("email = ? OR subject = ?", email, email).Limit(1).Find(&user).Error; err != nil {
			return err
		}
		if user.ID == "" {
			id, err := randomURLToken(18)
			if err != nil {
				return err
			}
			verifiedAt := s.now()
			user = sqlite.UserRecord{ID: id, Subject: email, Email: &email, EmailVerifiedAt: &verifiedAt}
			if err := tx.Create(&user).Error; err != nil {
				return err
			}
			if err := identity.AssignDefaultMemberRole(tx, user.ID); err != nil {
				return err
			}
		} else {
			verifiedAt := s.now()
			updates := map[string]any{"email_verified_at": verifiedAt}
			if user.Email == nil {
				updates["email"] = email
			}
			if err := tx.Model(&user).Updates(updates).Error; err != nil {
				return err
			}
		}
		id, err := randomURLToken(18)
		if err != nil {
			return err
		}
		link := sqlite.OAuthIdentityRecord{ID: id, UserID: user.ID, Provider: provider, ProviderSubject: providerSubject, CreatedAt: s.now()}
		if err := tx.Create(&link).Error; err != nil {
			return err
		}
		userID = user.ID
		return nil
	})
	return userID, err
}

func normalizeVerifiedEmail(values ...string) (string, string, error) {
	if len(values) == 0 {
		return "", "", ErrProviderUnavailable
	}
	var subject, email string
	if len(values) == 1 {
		email = values[0]
	} else {
		subject, email = values[0], values[1]
	}
	email = strings.ToLower(strings.TrimSpace(email))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || len(email) > 254 {
		return "", "", ErrProviderUnavailable
	}
	return subject, email, nil
}

func decodeJWTPart(part string, target any) error {
	data, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func randomURLToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func sumBytes(value string) []byte { sum := sha256.Sum256([]byte(value)); return sum[:] }
func hash(value string) string     { sum := sumBytes(value); return hex.EncodeToString(sum) }
