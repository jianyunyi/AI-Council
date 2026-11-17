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
	compat  bool
}

func New(cfg Config) *Provider {
	compat := cfg.BaseURL != ""
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	return &Provider{apiKey: cfg.APIKey, baseURL: cfg.BaseURL, model: cfg.Model, client: cfg.HTTPClient, compat: compat}
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
	if p.compat {
		return p.generateChatCompletion(ctx, req)
	}
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

func (p *Provider) generateChatCompletion(ctx context.Context, req provider.Request) (provider.Response, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	messages := make([]map[string]string, 0, len(req.Messages))
	for _, message := range req.Messages {
		messages = append(messages, map[string]string{"role": message.Role, "content": message.Content})
	}
	payload := map[string]any{"model": model, "messages": messages}
	if req.Temperature != 0 {
		payload["temperature"] = req.Temperature
	}
	var out struct {
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
	if err := httputil.DoJSON(ctx, p.client, http.MethodPost, httputil.Endpoint(p.baseURL, "chat/completions"), p.apiKey, payload, &out, p.Name()); err != nil {
		return provider.Response{}, err
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content == "" {
		return provider.Response{}, &provider.APIError{Provider: p.Name(), Kind: provider.ErrInvalidOutput, Message: "response contained no text"}
	}
	return provider.Response{Content: []byte(out.Choices[0].Message.Content), ProviderRequestID: out.ID, Usage: provider.Usage{InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens}}, nil
}
