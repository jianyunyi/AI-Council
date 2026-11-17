package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aicouncil/aicouncil/internal/provider"
	"github.com/stretchr/testify/require"
)

func TestCustomBaseURLUsesChatCompletions(t *testing.T) {
	var path string
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		require.Equal(t, "Bearer router-test-key", r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chat-1","choices":[{"message":{"content":"{\"ok\":true}"}}],"usage":{"prompt_tokens":4,"completion_tokens":6}}`))
	}))
	defer server.Close()

	client := New(Config{APIKey: "router-test-key", BaseURL: server.URL + "/api/v1/", Model: "test/free"})
	result, err := client.Generate(context.Background(), provider.Request{Messages: []provider.Message{{Role: "user", Content: "hi"}}})
	require.NoError(t, err)
	require.Equal(t, "/api/v1/chat/completions", path)
	require.Equal(t, "test/free", request["model"])
	require.JSONEq(t, `[{"role":"user","content":"hi"}]`, mustJSON(t, request["messages"]))
	require.JSONEq(t, `{"ok":true}`, string(result.Content))
	require.Equal(t, int64(4), result.Usage.InputTokens)
	require.Equal(t, int64(6), result.Usage.OutputTokens)
}

func TestDefaultBaseURLKeepsResponsesAPI(t *testing.T) {
	var path string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		path = r.URL.Path
		recorder := httptest.NewRecorder()
		recorder.Header().Set("Content-Type", "application/json")
		_, _ = recorder.WriteString(`{"id":"resp-1","output":[{"content":[{"text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":2}}`)
		return recorder.Result(), nil
	})}
	_, err := New(Config{APIKey: "test", Model: "gpt-test", HTTPClient: client}).Generate(context.Background(), provider.Request{Messages: []provider.Message{{Role: "user", Content: "hi"}}})
	require.NoError(t, err)
	require.Equal(t, "/v1/responses", path)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}
