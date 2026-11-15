package httputil

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aicouncil/aicouncil/internal/provider"
)

type retryTestTransport func(*http.Request) (*http.Response, error)

func (f retryTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestRetryRecordsProviderRetryWithoutNetwork(t *testing.T) {
	calls, retries := 0, 0
	client := &http.Client{Transport: retryTestTransport(func(*http.Request) (*http.Response, error) {
		calls++
		status, body := http.StatusOK, `{}`
		if calls == 1 {
			status, body = http.StatusTooManyRequests, `{"error":"retry"}`
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	ctx := provider.WithRetryCallback(context.Background(), func() { retries++ })
	var output map[string]any
	err := DoJSONWithHeaders(ctx, client, http.MethodGet, "https://provider.invalid", nil, nil, &output, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || retries != 1 {
		t.Fatalf("calls=%d retries=%d, want 2 calls and 1 retry", calls, retries)
	}
}
