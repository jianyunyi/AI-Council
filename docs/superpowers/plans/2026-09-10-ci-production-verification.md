# CI Production Verification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make each pull request, push, and manually dispatched release-readiness run verify the product source and production Compose images without deploying or exposing credentials.

**Architecture:** Keep `.github/workflows/ci.yml` as one workflow. Independent Go, Web, and deployment jobs feed an image-build job; a `workflow_dispatch`-only job then waits on GitHub's `production` Environment approval. A dependency-free PowerShell deployment verifier replaces the Pester-only check so Linux GitHub runners can execute the same assertions without installing a module.

**Tech Stack:** GitHub Actions, Ubuntu runners, Go 1.25, Node 22, pnpm 9.15, Playwright Chromium, Docker Compose v2, PowerShell 7.

---

### Task 1: Make deployment configuration verification runner-portable

**Files:**
- Create: `deploy/verify-compose-build-config.ps1`
- Delete: `deploy/compose_build_args.tests.ps1`
- Test: `deploy/verify-compose-build-config.ps1`

- [ ] **Step 1: Write the dependency-free verifier before deleting the Pester test.**

```powershell
[CmdletBinding()]
param()

function Assert-Match([string]$Content, [string]$Pattern, [string]$Description) {
    if ($Content -notmatch $Pattern) {
        throw "Deployment configuration check failed: $Description"
    }
}

foreach ($file in 'Dockerfile.council', 'Dockerfile.runner') {
    $content = Get-Content -Raw (Join-Path $PSScriptRoot $file)
    Assert-Match $content '(?m)^ARG GOPROXY=https://goproxy\.cn,direct$' "$file declares GOPROXY"
    Assert-Match $content '(?m)^ENV GOPROXY=\$GOPROXY$' "$file exports GOPROXY"
}

$compose = Get-Content -Raw (Join-Path $PSScriptRoot 'docker-compose.yml')
foreach ($service in 'council', 'runner') {
    Assert-Match $compose "(?ms)^  ${service}:\s+build:.*?args:\s+GOPROXY: \$\{GOPROXY:-https://goproxy\.cn,direct\}" "$service receives GOPROXY"
}

$validator = Get-Content -Raw (Join-Path $PSScriptRoot 'validate-compose.ps1')
Assert-Match $validator '(?m)config --quiet$' 'Compose validation is quiet'
& (Join-Path $PSScriptRoot 'validate-compose.ps1')
```

- [ ] **Step 2: Run the verifier and confirm it passes against the current deployment files.**

Run:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File deploy/verify-compose-build-config.ps1
```

Expected: exit code `0`; Docker Compose config is parsed without interpolated values printed.

- [ ] **Step 3: Remove the Pester-specific test after the portable verifier passes.**

Run:

```powershell
Remove-Item -LiteralPath deploy/compose_build_args.tests.ps1
```

- [ ] **Step 4: Re-run the portable verifier after removing Pester.**

Run:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File deploy/verify-compose-build-config.ps1
```

Expected: exit code `0`; no Pester module is required.

- [ ] **Step 5: Commit the isolated verifier migration.**

```powershell
git add deploy/verify-compose-build-config.ps1 deploy/compose_build_args.tests.ps1
git commit -m "test: make deployment configuration checks portable"
```

### Task 2: Extend the single CI workflow

**Files:**
- Modify: `.github/workflows/ci.yml`
- Test: GitHub Actions workflow run on a pull request or branch push

- [ ] **Step 1: Add manual dispatch and explicit least-privilege permissions.**

```yaml
name: ci

on:
  push:
    branches: ["**"]
  pull_request:
  workflow_dispatch:

permissions:
  contents: read
```

- [ ] **Step 2: Make the Go job deterministic and cached.**

```yaml
  go:
    runs-on: ubuntu-latest
    env:
      GOPROXY: https://proxy.golang.org,direct
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25.x'
          cache: true
      - run: go test ./...
      - run: go vet ./...
```

- [ ] **Step 3: Make the Web job run unit tests before the production build.**

```yaml
  web:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: pnpm/action-setup@v4
        with:
          version: 9.15.0
      - uses: actions/setup-node@v4
        with:
          node-version: 22
          cache: pnpm
          cache-dependency-path: web/pnpm-lock.yaml
      - run: pnpm --dir web install --frozen-lockfile
      - run: pnpm --dir web test
      - run: pnpm --dir web build
```

- [ ] **Step 4: Add an independent deployment verification job.**

```yaml
  deployment:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Verify deployment configuration
        shell: pwsh
        run: ./deploy/verify-compose-build-config.ps1
```

- [ ] **Step 5: Gate all local image builds on successful verification and use fixed non-secret Compose values.**

```yaml
  images:
    runs-on: ubuntu-latest
    needs: [go, web, e2e, deployment]
    env:
      COUNCIL_BOOTSTRAP_SUBJECT: ci-validation
      COUNCIL_BOOTSTRAP_PASSWORD: validation-only-password
      RUNNER_TOKEN: validation-only-runner-token
      TLS_CERT: /certs/validation.crt
      TLS_KEY: /certs/validation.key
      GOPROXY: https://proxy.golang.org,direct
    steps:
      - uses: actions/checkout@v4
      - name: Build production images without publishing
        run: docker compose --env-file deploy/.env.example -f deploy/docker-compose.yml build
```

- [ ] **Step 6: Add a manual approval-only release-readiness job.**

```yaml
  release-readiness:
    if: github.event_name == 'workflow_dispatch'
    runs-on: ubuntu-latest
    needs: images
    environment:
      name: production
    steps:
      - name: Record approved revision
        env:
          REVISION: ${{ github.sha }}
        run: |
          echo "## Production release readiness" >> "$GITHUB_STEP_SUMMARY"
          echo "Verified revision: \`$REVISION\`" >> "$GITHUB_STEP_SUMMARY"
          echo "No image was pushed and no deployment was performed." >> "$GITHUB_STEP_SUMMARY"
```

- [ ] **Step 7: Review the workflow for credential boundaries.**

Run:

```powershell
rg -n 'secrets\.|password=|token=' .github/workflows/ci.yml
```

Expected: no `secrets.` reference; the only password/token strings are fixed validation values in the `images` job.

- [ ] **Step 8: Commit the CI workflow.**

```powershell
git add .github/workflows/ci.yml
git commit -m "ci: verify production deployment and image builds"
```

### Task 3: Document CI approval configuration and local equivalence

**Files:**
- Modify: `README.md`
- Modify: `docs/deployment/compose-production.md`
- Test: Markdown links and command paths are readable

- [ ] **Step 1: Add a CI section to the README.**

Document the four automatic gates (Go, Web, E2E, deployment/image build), state that no image is pushed, and point operators to the Compose deployment guide for actual deployment.

- [ ] **Step 2: Add a release-readiness section to the Compose guide.**

Include the exact GitHub UI sequence: Actions → `ci` → Run workflow → select `master` → Run workflow; configure the repository `production` Environment with required reviewers before using it. State that approval marks only a verified revision and never starts a server deployment.

- [ ] **Step 3: Verify documentation references.**

Run:

```powershell
rg -n 'workflow_dispatch|production|verify-compose-build-config|docker compose' README.md docs/deployment/compose-production.md
```

Expected: references use the actual workflow, environment, verifier, and Compose command names.

- [ ] **Step 4: Commit the documentation.**

```powershell
git add README.md docs/deployment/compose-production.md
git commit -m "docs: explain CI release readiness approval"
```

### Task 4: Verify the complete CI change locally and remotely

**Files:**
- Verify: `.github/workflows/ci.yml`
- Verify: `deploy/verify-compose-build-config.ps1`
- Verify: `go.mod`, `web/package.json`

- [ ] **Step 1: Run local Go verification.**

Run:

```powershell
$env:GOCACHE = "$PWD\.tmp-go-cache"
$env:GOMODCACHE = "$PWD\.tmp-go-mod"
$env:GOPROXY = 'https://goproxy.cn,direct'
go test ./... -count=1 -p 1
go vet ./...
```

Expected: both commands exit `0`.

- [ ] **Step 2: Run the local Web suite when pnpm is available.**

Run:

```powershell
pnpm --dir web install --frozen-lockfile
pnpm --dir web test
pnpm --dir web build
```

Expected: Vitest and the Linux-compatible production build exit `0`.

- [ ] **Step 3: Run the deployment verifier.**

Run:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File deploy/verify-compose-build-config.ps1
git diff --check
```

Expected: both commands exit `0`.

- [ ] **Step 4: Push and inspect the GitHub Actions run.**

Run:

```powershell
git push origin master
```

Expected: `go`, `web`, `e2e`, `deployment`, and `images` finish successfully. Trigger the workflow manually only after configuring the GitHub `production` Environment required reviewer; `release-readiness` then waits for approval and writes its summary without deployment.
