package httputil

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fixedTransport func(*http.Request) (*http.Response, error)

func (f fixedTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A >64KB successful body must decode intact rather than being truncated at the
// old 2KB error ceiling.
func TestDoJSONDecodesLargeSuccessBody(t *testing.T) {
	filler := strings.Repeat("a", 200*1024) // 200KB, well past 64KB and the old 2KB cap
	payload := `{"text":"` + filler + `"}`
	client := &http.Client{Transport: fixedTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload))}, nil
	})}
	var out struct {
		Text string `json:"text"`
	}
	if err := DoJSONWithHeaders(context.Background(), client, http.MethodPost, "https://provider.invalid", nil, nil, &out, "openai"); err != nil {
		t.Fatalf("decode large success body: %v", err)
	}
	if len(out.Text) != len(filler) {
		t.Fatalf("decoded text length = %d, want %d", len(out.Text), len(filler))
	}
}

// A body beyond the success ceiling yields an explicit error, never a silent
// truncation that would masquerade as a malformed-JSON decode failure.
func TestDoJSONRejectsBodyBeyondCeiling(t *testing.T) {
	t.Setenv("AICOUNCIL_MAX_RESPONSE_BYTES", "128")
	client := &http.Client{Transport: fixedTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"text":"` + strings.Repeat("a", 4096) + `"}`))}, nil
	})}
	var out map[string]any
	err := DoJSONWithHeaders(context.Background(), client, http.MethodPost, "https://provider.invalid", nil, nil, &out, "openai")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("want response-too-large error, got %v", err)
	}
}

// Error responses keep the small ceiling and remain sanitized.
func TestDoJSONErrorBodyStaysBoundedAndSanitized(t *testing.T) {
	client := &http.Client{Transport: fixedTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"api_key":"sk-secret","error":"bad"}`))}, nil
	})}
	var out map[string]any
	err := DoJSONWithHeaders(context.Background(), client, http.MethodPost, "https://provider.invalid", nil, nil, &out, "openai")
	if err == nil || strings.Contains(err.Error(), "sk-secret") {
		t.Fatalf("error body must be sanitized, got %v", err)
	}
}
