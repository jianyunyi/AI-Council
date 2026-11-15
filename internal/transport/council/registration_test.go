package council

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/aicouncil/aicouncil/internal/security/identity"
	"github.com/aicouncil/aicouncil/internal/security/rbac"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
)

type captureMailer struct {
	to, subject, text string
	err               error
}

func (m *captureMailer) Send(_ context.Context, to, subject, text string) error {
	m.to, m.subject, m.text = to, subject, text
	return m.err
}

func TestRegistrationAndVerificationRoutesArePublicAndSendOneTimeToken(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "accounts.db"))
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	mail := &captureMailer{}
	api := NewRBACAPIWithIdentity(rbac.New(db), SessionOptions{CookieSecure: false}, identity.New(db), mail)
	register := routeHandler(t, api.Routes(), http.MethodPost, "/api/v1/auth/register")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewBufferString(`{"email":"alice@example.com","password":"correct horse battery staple"}`))
	rec := httptest.NewRecorder()
	RBACAuth(rbac.New(db), "")(register)(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)
	require.Equal(t, "alice@example.com", mail.to)
	token := mail.text[len("Your one-time verification token is: "):]
	verify := routeHandler(t, api.Routes(), http.MethodPost, "/api/v1/auth/verify-email")
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify-email", bytes.NewBufferString(`{"token":"`+token+`"}`))
	rec = httptest.NewRecorder()
	RBACAuth(rbac.New(db), "")(verify)(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	_, _, err = rbac.New(db).Login(context.Background(), "alice@example.com", "correct horse battery staple", time.Hour)
	require.NoError(t, err)
}

func TestPasswordResetRequestResponseDoesNotRevealUnknownEmail(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "accounts.db"))
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	mail := &captureMailer{}
	api := NewRBACAPIWithIdentity(rbac.New(db), SessionOptions{}, identity.New(db), mail)
	handler := routeHandler(t, api.Routes(), http.MethodPost, "/api/v1/auth/password-reset/request")
	call := func(email string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password-reset/request", bytes.NewBufferString(`{"email":"`+email+`"}`))
		rec := httptest.NewRecorder()
		RBACAuth(rbac.New(db), "")(handler)(rec, req)
		return rec
	}
	_, err = identity.New(db).RegisterEmail(context.Background(), "known@example.com", "correct horse battery staple")
	require.NoError(t, err)
	known := call("known@example.com")
	unknown := call("unknown@example.com")
	require.Equal(t, known.Code, unknown.Code)
	require.Contains(t, known.Body.String(), `"status":"if_account_exists_email_sent"`)
	require.Contains(t, unknown.Body.String(), `"status":"if_account_exists_email_sent"`)
}
