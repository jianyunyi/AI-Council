package httputil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/aicouncil/aicouncil/internal/provider"
)

const maxErrorBody = 2048

func Endpoint(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

func DoJSON(ctx context.Context, client *http.Client, method, url, token string, payload any, out any, provider string) error {
	return DoJSONWithHeaders(ctx, client, method, url, map[string]string{"Authorization": "Bearer " + token}, payload, out, provider)
}

func DoJSONWithHeaders(ctx context.Context, client *http.Client, method, url string, headers map[string]string, payload any, out any, providerName string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%s: encode request: %w", providerName, err)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: create request: %w", providerName, err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		if value != "" {
			req.Header.Set(key, value)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: request failed: %w", providerName, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody+1))
	if err != nil {
		return fmt.Errorf("%s: read response: %w", providerName, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		kind := error(nil)
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			kind = provider.ErrUnauthorized
		case http.StatusTooManyRequests:
			kind = provider.ErrRateLimited
		}
		msg := SanitizeBody(data)
		if len(msg) == 0 {
			msg = http.StatusText(resp.StatusCode)
		}
		return &provider.APIError{Provider: providerName, Status: resp.StatusCode, Kind: kind, Message: msg}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s: decode response: %w", providerName, err)
	}
	return nil
}

func SanitizeBody(data []byte) string {
	s := string(data)
	if len(s) > maxErrorBody {
		s = s[:maxErrorBody] + "…"
	}
	for _, key := range []string{"Authorization", "authorization", "api_key", "apiKey"} {
		lower := strings.ToLower(s)
		if i := strings.Index(lower, strings.ToLower(key)); i >= 0 {
			end := strings.IndexAny(s[i:], ",}\n")
			if end < 0 {
				end = len(s) - i
			}
			s = s[:i] + key + ": [REDACTED]" + s[i+end:]
		}
	}
	return s
}
