package council

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aicouncil/aicouncil/internal/app/group"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/provider"
	"github.com/aicouncil/aicouncil/internal/provider/anthropic"
	"github.com/aicouncil/aicouncil/internal/provider/deepseek"
	"github.com/aicouncil/aicouncil/internal/provider/openai"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// groupProviderAdapter implements group.ProviderInvoker over the REAL
// profile -> provider mechanism (WIRING W6 of the P6 plan): the same one
// the council factory in cmd/council-server uses for role-assigned seats.
// An AI model's registry entry names a provider profile; the profile's
// owner + provider type come from the profile record, its API key is
// decrypted via providerkeys.Service.Secret, and the provider is built by
// the per-type constructors and wrapped in the cost meter. The Quick
// draft's providerForProfile helper does not exist and nothing here
// invents a parallel mechanism.
type groupProviderAdapter struct{ a *API }

func (ad groupProviderAdapter) Generate(ctx context.Context, req group.InvokeRequest) (group.InvokeResult, error) {
	a := ad.a
	if a.aiModels == nil || a.db == nil {
		return group.InvokeResult{}, errors.New("ai model registry is not available on this server")
	}
	view, err := a.aiModels.Get(ctx, req.AIModelID)
	if err != nil {
		return group.InvokeResult{}, fmt.Errorf("resolve ai model %s: %w", req.AIModelID, err)
	}
	if a.providerKeys == nil {
		return group.InvokeResult{}, errors.New("provider keys are not configured on this server (PROVIDER_KEY_ENCRYPTION_KEY is unset)")
	}
	var profile sqlite.ProviderProfileRecord
	if err := a.db.WithContext(ctx).Where("id = ?", view.ProviderProfileID).First(&profile).Error; err != nil {
		return group.InvokeResult{}, fmt.Errorf("load provider profile %s: %w", view.ProviderProfileID, err)
	}
	secret, err := a.providerKeys.Secret(ctx, profile.OwnerUserID, profile.ID)
	if err != nil {
		return group.InvokeResult{}, fmt.Errorf("decrypt provider key: %w", err)
	}
	var p provider.ModelProvider
	switch profile.Provider {
	case "openai":
		p = openai.New(openai.Config{APIKey: secret, Model: view.Model})
	case "deepseek":
		p = deepseek.New(deepseek.Config{APIKey: secret, Model: view.Model})
	case "anthropic":
		p = anthropic.New(anthropic.Config{APIKey: secret, Model: view.Model})
	default:
		return group.InvokeResult{}, fmt.Errorf("unsupported provider %q", profile.Provider)
	}
	p = provider.WithCostMeter(p, a.Metrics())

	msgs := make([]provider.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, provider.Message{Role: m.Role, Content: m.Content})
	}
	preq := provider.Request{Model: view.Model, Messages: msgs, Temperature: req.Temperature}
	if req.JSONMode {
		// The council engine embeds an example-struct schema the same way
		// (json.Marshal of the empty struct); providers that support
		// structured output use it, the rest rely on the prompt.
		if schemaJSON, err := json.Marshal(schema.ExecutionPlan{}); err == nil {
			preq.JSONSchema = schemaJSON
		}
	}
	resp, err := p.Generate(ctx, preq)
	if err != nil {
		return group.InvokeResult{}, err
	}
	return group.InvokeResult{
		Content:           string(resp.Content),
		InputTokens:       resp.Usage.InputTokens,
		OutputTokens:      resp.Usage.OutputTokens,
		ProviderRequestID: resp.ProviderRequestID,
	}, nil
}

// GenerateForProfile implements group.ProfileProviderInvoker (P8): a
// red-team seat names a provider profile + model directly (Part A role
// assignment), which may not be a registered AI model, so Generate's
// AIModelID lookup cannot serve it. The construction is identical to
// Generate's — profile record, decrypted secret, per-type provider,
// cost meter — with the seat's model instead of the registry's.
func (ad groupProviderAdapter) GenerateForProfile(ctx context.Context, profileID, model string, req group.InvokeRequest) (group.InvokeResult, error) {
	a := ad.a
	if a.db == nil {
		return group.InvokeResult{}, errors.New("storage is not available on this server")
	}
	if a.providerKeys == nil {
		return group.InvokeResult{}, errors.New("provider keys are not configured on this server (PROVIDER_KEY_ENCRYPTION_KEY is unset)")
	}
	var profile sqlite.ProviderProfileRecord
	if err := a.db.WithContext(ctx).Where("id = ?", profileID).First(&profile).Error; err != nil {
		return group.InvokeResult{}, fmt.Errorf("load provider profile %s: %w", profileID, err)
	}
	secret, err := a.providerKeys.Secret(ctx, profile.OwnerUserID, profile.ID)
	if err != nil {
		return group.InvokeResult{}, fmt.Errorf("decrypt provider key: %w", err)
	}
	var p provider.ModelProvider
	switch profile.Provider {
	case "openai":
		p = openai.New(openai.Config{APIKey: secret, Model: model})
	case "deepseek":
		p = deepseek.New(deepseek.Config{APIKey: secret, Model: model})
	case "anthropic":
		p = anthropic.New(anthropic.Config{APIKey: secret, Model: model})
	default:
		return group.InvokeResult{}, fmt.Errorf("unsupported provider %q", profile.Provider)
	}
	p = provider.WithCostMeter(p, a.Metrics())

	msgs := make([]provider.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, provider.Message{Role: m.Role, Content: m.Content})
	}
	preq := provider.Request{Model: model, Messages: msgs, Temperature: req.Temperature}
	if req.JSONMode {
		// Approximate schema hint, as in Generate; the group service
		// parses red-team output tolerantly (extractJSONObject).
		if schemaJSON, err := json.Marshal(schema.RedTeamReport{}); err == nil {
			preq.JSONSchema = schemaJSON
		}
	}
	resp, err := p.Generate(ctx, preq)
	if err != nil {
		return group.InvokeResult{}, err
	}
	return group.InvokeResult{
		Content:           string(resp.Content),
		InputTokens:       resp.Usage.InputTokens,
		OutputTokens:      resp.Usage.OutputTokens,
		ProviderRequestID: resp.ProviderRequestID,
	}, nil
}
