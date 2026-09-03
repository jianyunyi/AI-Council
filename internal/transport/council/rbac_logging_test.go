package council

import (
	"bytes"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/rest"
)

func TestRBACServerErrorLogsDoNotLeakCredentials(t *testing.T) {
	// These loggers are process-wide, so this test must not run in parallel.
	var output rbacLogBuffer
	previousLogger := slog.Default()
	previousOutput, previousFlags := log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previousLogger)
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
	})

	conf := rest.RestConf{Host: "127.0.0.1"}
	conf.Middlewares.Log = true // The RBAC constructor must override this unsafe default.
	server := NewServerWithAPIAndRBACWithOptions(conf, NewAPI(), nil, "", SessionOptions{})
	previousWriter := logx.Reset()
	logx.SetWriter(logx.NewWriter(&output))
	t.Cleanup(func() { logx.SetWriter(previousWriter) })

	// Inject a downstream failure after RBAC authentication, without replacing
	// any of the framework or application middleware configured by the server.
	server.Use(func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/auth/login" {
				next(w, r)
				return
			}
			_, _ = io.Copy(io.Discard, r.Body)
			http.Error(w, "injected failure", http.StatusInternalServerError)
		}
	})
	testServer := httptest.NewServer(boundRBACHandler(t, server))
	t.Cleanup(testServer.Close)

	const bearer = "rbac-bearer-secret-canary"
	const cookie = "rbac-cookie-secret-canary"
	const password = "rbac-password-secret-canary"
	request, err := http.NewRequest(http.MethodPost, testServer.URL+"/api/v1/auth/login",
		strings.NewReader(`{"subject":"alice","password":"`+password+`"}`))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.AddCookie(&http.Cookie{Name: "aicouncil_session", Value: cookie})
	response, err := testServer.Client().Do(request)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, response.Body.Close())
	require.NoError(t, err)
	require.Equal(t, http.StatusInternalServerError, response.StatusCode)
	// Wait for the entire middleware chain, including post-response log writes.
	testServer.Close()

	logs := output.String()
	require.Contains(t, logs, `"msg":"http_request"`)
	require.Contains(t, logs, `"method":"POST"`)
	require.Contains(t, logs, `"path":"/api/v1/auth/login"`)
	require.Contains(t, logs, `"duration_ms":`)
	for _, secret := range []string{bearer, cookie, password} {
		if strings.Contains(logs, secret) {
			t.Errorf("HTTP 500 request logs leaked credential canary %q", secret)
		}
	}
}

func boundRBACHandler(t *testing.T, server *rest.Server) (handler http.Handler) {
	t.Helper()
	// StartWithOpts binds the real route middleware before invoking this option.
	// Stop there: go-zero's normal shutdown waits for a process-wide signal.
	// Serving the bound handler with httptest avoids a leaked listener/goroutine.
	stop := new(struct{})
	func() {
		defer func() {
			if recovered := recover(); recovered != stop {
				panic(recovered)
			}
		}()
		server.StartWithOpts(func(s *http.Server) {
			handler = s.Handler
			panic(stop)
		})
	}()
	require.NotNil(t, handler)
	return handler
}

type rbacLogBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *rbacLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *rbacLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
