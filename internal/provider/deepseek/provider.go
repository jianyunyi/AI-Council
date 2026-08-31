package deepseek

import (
	"context"
	"net/http"

	"github.com/aicouncil/aicouncil/internal/provider"
	"github.com/aicouncil/aicouncil/internal/provider/httputil"
)

type Config struct {
	APIKey, BaseURL, Model string
	HTTPClient             *http.Client
}

type Provider struct {
	apiKey, baseURL, model string
	client                 *http.Client
}

func New(cfg Config) *Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.deepseek.com/v1"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	return &Provider{apiKey: cfg.APIKey, baseURL: cfg.BaseURL, model: cfg.Model, client: cfg.HTTPClient}
}

func (p *Provider) Name() string { return "deepseek" }

type response struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
}

func (p *Provider) Generate(ctx context.Context, req provider.Request) (provider.Response, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	payload := map[string]any{"model": model, "messages": req.Messages, "response_format": map[string]string{"type": "json_object"}}
	if req.Temperature != 0 {
		payload["temperature"] = req.Temperature
	}
	var out response
	if err := httputil.DoJSON(ctx, p.client, http.MethodPost, httputil.Endpoint(p.baseURL, "chat/completions"), p.apiKey, payload, &out, p.Name()); err != nil {
		return provider.Response{}, err
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content == "" {
		return provider.Response{}, &provider.APIError{Provider: p.Name(), Kind: provider.ErrInvalidOutput, Message: "response contained no choices"}
	}
	return provider.Response{Content: []byte(out.Choices[0].Message.Content), ProviderRequestID: out.ID, Usage: provider.Usage{InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens}}, nil
}
