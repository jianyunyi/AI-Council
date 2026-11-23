package httputil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aicouncil/aicouncil/internal/provider"
)

const maxErrorBody = 2048

// defaultMaxResponseBytes bounds a *successful* provider response body. The
// council only ever sends a bounded workspace snapshot, so a generated plan is
// far smaller than this; the ceiling exists to stop a pathological or hostile
// response from exhausting memory. Override with AICOUNCIL_MAX_RESPONSE_BYTES.
const defaultMaxResponseBytes int64 = 32 << 20

// ErrResponseTooLarge is returned when a successful response exceeds the
// configured success ceiling. It is deliberately explicit rather than a silent
// truncation that would later fail to decode.
var errResponseTooLarge = func(providerName string, limit int64) error {
	return fmt.Errorf("%s: response exceeds %d bytes", providerName, limit)
}

func maxResponseBytes() int64 {
	raw := strings.TrimSpace(os.Getenv("AICOUNCIL_MAX_RESPONSE_BYTES"))
	if raw == "" {
		return defaultMaxResponseBytes
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return defaultMaxResponseBytes
	}
	return value
}

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
	successLimit := maxResponseBytes()
	var resp *http.Response
	var data []byte
	var tooLarge bool
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 && req.GetBody != nil {
			req.Body, _ = req.GetBody()
		}
		resp, err = client.Do(req)
		if err != nil {
			return fmt.Errorf("%s: request failed: %w", providerName, err)
		}
		// Non-2xx responses are read with the small error ceiling (and later
		// sanitized). A successful body is read up to the success ceiling so
		// large-but-valid plans are not silently truncated into decode errors.
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			data, err = io.ReadAll(io.LimitReader(resp.Body, maxErrorBody+1))
		} else {
			data, err = io.ReadAll(io.LimitReader(resp.Body, successLimit+1))
			tooLarge = err == nil && int64(len(data)) > successLimit
		}
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("%s: read response: %w", providerName, err)
		}
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			break
		}
		if attempt < 2 {
			provider.RecordRetry(ctx)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * 100 * time.Millisecond):
			}
		}
	}
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
	if tooLarge {
		return errResponseTooLarge(providerName, successLimit)
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
