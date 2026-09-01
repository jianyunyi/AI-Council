package openai

import (
	"context"
	"net/http"

	"github.com/aicouncil/aicouncil/internal/provider"
	"github.com/aicouncil/aicouncil/internal/provider/httputil"
)

type Config struct {
	APIKey     string
	BaseURL    string
	Model      string
	HTTPClient *http.Client
}

type Provider struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

func New(cfg Config) *Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	return &Provider{apiKey: cfg.APIKey, baseURL: cfg.BaseURL, model: cfg.Model, client: cfg.HTTPClient}
}

func (p *Provider) Name() string { return "openai" }

type response struct {
	ID     string `json:"id"`
	Output []struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
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
	input := make([]map[string]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		input = append(input, map[string]string{"role": m.Role, "content": m.Content})
	}
	payload := map[string]any{"model": model, "input": input}
	if req.Temperature != 0 {
		payload["temperature"] = req.Temperature
	}
	var out response
	if err := httputil.DoJSON(ctx, p.client, http.MethodPost, httputil.Endpoint(p.baseURL, "responses"), p.apiKey, payload, &out, p.Name()); err != nil {
		return provider.Response{}, err
	}
	var text string
	for _, item := range out.Output {
		for _, c := range item.Content {
			if c.Text != "" {
				text += c.Text
			}
		}
	}
	if text == "" {
		return provider.Response{}, &provider.APIError{Provider: p.Name(), Kind: provider.ErrInvalidOutput, Message: "response contained no text"}
	}
	return provider.Response{Content: []byte(text), ProviderRequestID: out.ID, Usage: provider.Usage{InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens}}, nil
}
