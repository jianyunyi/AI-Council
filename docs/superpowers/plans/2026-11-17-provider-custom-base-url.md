# Provider Custom Base URL Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Let each OpenAI-compatible provider profile route to a validated HTTPS API base URL while preserving encrypted key storage and existing defaults.

**Architecture:** Persist an optional normalized base URL on the provider profile record and include it in public profile metadata. Pass the profile URL into the existing OpenAI adapter; an empty value keeps the provider default. REST and web forms expose the field only for OpenAI-compatible profiles.

**Tech Stack:** Go, GORM/SQLite, Gin/REST transport, Next.js/React, Vitest.

---

### Task 1: Validate and persist profile URLs

**Files:** `internal/storage/sqlite/models.go`, `internal/security/providerkeys/service.go`, `internal/security/providerkeys/service_test.go`

- [x] Add failing tests for valid OpenRouter URL normalization, invalid HTTP/userinfo/fragment/relative URLs, and empty default URL.
- [x] Add `BaseURL` to the SQLite profile record and public profile; accept it in `Create` and validate only for `openai` profiles.
- [x] Run `go test ./internal/security/providerkeys ./internal/storage/sqlite`.

### Task 2: Route OpenAI-compatible calls to the profile URL

**Files:** `cmd/council-server/main.go`, `internal/provider/openai/provider.go`, tests in `cmd/council-server` or provider package.

- [x] Add a failing test proving an OpenRouter profile uses its base URL and never falls back to the OpenAI host.
- [x] Pass profile BaseURL to the OpenAI adapter while preserving empty-URL defaults and fixed DeepSeek/Anthropic endpoints.
- [x] Run targeted provider and server tests.

### Task 3: Expose the field through REST

**Files:** `internal/transport/council/providerkey_routes.go`, `internal/transport/council` provider key tests.

- [x] Add failing REST cases for accepted HTTPS URL, rejected non-HTTPS URL, and returned base URL without key plaintext.
- [x] Decode and pass `base_url` through create route; keep current error envelope and owner-scoped behavior.
- [x] Run targeted Council transport tests.

### Task 4: Add the provider URL control to the web UI

**Files:** `web/lib/api.ts`, `web/app/providers/page.tsx`, `web/app/providers/page.test.tsx`.

- [x] Add a failing UI test for OpenAI URL entry, persisted profile display, and hidden field on Anthropic/DeepSeek selections.
- [x] Update types and API payload; default the input to blank and explain OpenRouter example URL/model configuration.
- [x] Run `pnpm --dir web test -- app/providers/page.test.tsx` and `pnpm --dir web build`.

### Task 5: Full verification

**Files:** Provider and operations docs as needed.

- [x] Run `go test ./...`, `go vet ./...`, `pnpm --dir web test`, `pnpm --dir web build`, and `git diff --check`.
- [x] Document that live Provider verification requires a newly rotated, low-limit key entered only on the target host.
- [x] Review diff for key leakage, URL leakage, unrelated files, and migration compatibility.

