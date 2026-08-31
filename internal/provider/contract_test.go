package provider_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aicouncil/aicouncil/internal/provider"
	"github.com/aicouncil/aicouncil/internal/provider/anthropic"
	"github.com/aicouncil/aicouncil/internal/provider/deepseek"
	"github.com/aicouncil/aicouncil/internal/provider/openai"
	"github.com/stretchr/testify/require"
)

func TestAdaptersNormalizeResponses(t *testing.T) {
	tests := []struct {
		name string
		new  func(string, *http.Client) provider.ModelProvider
		body string
	}{
		{"openai", func(base string, c *http.Client) provider.ModelProvider {
			return openai.New(openai.Config{APIKey: "test", BaseURL: base, Model: "gpt-test", HTTPClient: c})
		}, `{"id":"resp_1","output":[{"type":"message","content":[{"type":"output_text","text":"{\"ok\":true}"}]}],"usage":{"input_tokens":3,"output_tokens":5}}`},
		{"anthropic", func(base string, c *http.Client) provider.ModelProvider {
			return anthropic.New(anthropic.Config{APIKey: "test", BaseURL: base, Model: "claude-test", HTTPClient: c})
		}, `{"id":"msg_1","content":[{"type":"text","text":"{\"ok\":true}"}],"usage":{"input_tokens":3,"output_tokens":5}}`},
		{"deepseek", func(base string, c *http.Client) provider.ModelProvider {
			return deepseek.New(deepseek.Config{APIKey: "test", BaseURL: base, Model: "deepseek-test", HTTPClient: c})
		}, `{"id":"chat_1","choices":[{"message":{"content":"{\"ok\":true}"}}],"usage":{"prompt_tokens":3,"completion_tokens":5}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.NotEmpty(t, r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			resp, err := tt.new(srv.URL, srv.Client()).Generate(context.Background(), provider.Request{Messages: []provider.Message{{Role: "user", Content: "return json"}}})
			require.NoError(t, err)
			require.JSONEq(t, `{"ok":true}`, string(resp.Content))
			require.Equal(t, int64(3), resp.Usage.InputTokens)
			require.Equal(t, int64(5), resp.Usage.OutputTokens)
			require.NotEmpty(t, resp.ProviderRequestID)
		})
	}
}

func TestAdaptersNormalizeRateLimitErrors(t *testing.T) {
	tests := []struct {
		name string
		new  func(string, *http.Client) provider.ModelProvider
	}{
		{"openai", func(base string, c *http.Client) provider.ModelProvider {
			return openai.New(openai.Config{APIKey: "test", BaseURL: base, HTTPClient: c})
		}},
		{"anthropic", func(base string, c *http.Client) provider.ModelProvider {
			return anthropic.New(anthropic.Config{APIKey: "test", BaseURL: base, HTTPClient: c})
		}},
		{"deepseek", func(base string, c *http.Client) provider.ModelProvider {
			return deepseek.New(deepseek.Config{APIKey: "test", BaseURL: base, HTTPClient: c})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"slow down"}`))
			}))
			defer srv.Close()
			_, err := tt.new(srv.URL, srv.Client()).Generate(context.Background(), provider.Request{Messages: []provider.Message{{Role: "user", Content: "x"}}})
			require.Error(t, err)
			require.True(t, errors.Is(err, provider.ErrRateLimited), err)
		})
	}
}
