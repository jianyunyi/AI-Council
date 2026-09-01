package anthropic

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
		cfg.BaseURL = "https://api.anthropic.com/v1"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	return &Provider{apiKey: cfg.APIKey, baseURL: cfg.BaseURL, model: cfg.Model, client: cfg.HTTPClient}
}

func (p *Provider) Name() string { return "anthropic" }

type response struct {
	ID      string `json:"id"`
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

func (p *Provider) Generate(ctx context.Context, req provider.Request) (provider.Response, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	payload := map[string]any{"model": model, "max_tokens": 4096, "messages": req.Messages}
	if req.Temperature != 0 {
		payload["temperature"] = req.Temperature
	}
	var out response
	headers := map[string]string{"Authorization": "Bearer " + p.apiKey, "x-api-key": p.apiKey, "anthropic-version": "2023-06-01"}
	if err := httputil.DoJSONWithHeaders(ctx, p.client, http.MethodPost, httputil.Endpoint(p.baseURL, "messages"), headers, payload, &out, p.Name()); err != nil {
		return provider.Response{}, err
	}
	content := ""
	for _, item := range out.Content {
		content += item.Text
	}
	if content == "" {
		return provider.Response{}, &provider.APIError{Provider: p.Name(), Kind: provider.ErrInvalidOutput, Message: "response contained no text"}
	}
	return provider.Response{Content: []byte(content), ProviderRequestID: out.ID, Usage: provider.Usage{InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens}}, nil
}
