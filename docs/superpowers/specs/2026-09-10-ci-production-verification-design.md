# CI Production Verification Design

## Goal

Extend the existing GitHub Actions CI workflow so every pull request and push proves that the Go services, Next.js control plane, Compose deployment definition, and production container images remain buildable. A manually dispatched run adds a GitHub Environment approval gate, but it never deploys a service or reads provider credentials.

## Scope

- Keep `.github/workflows/ci.yml` as the single workflow.
- Run Go tests and vet on Ubuntu with the Go module cache enabled.
- Run Web dependency installation, Vitest, Next production build, and Playwright Chromium E2E on Ubuntu.
- Run the PowerShell deployment regression suite and `deploy/validate-compose.ps1` on Ubuntu.
- Build the `council`, `runner`, and `web` Compose images without pushing them.
- Add a `workflow_dispatch`-only release-readiness job protected by the GitHub `production` Environment.

Out of scope: registry publishing, server deployment, certificate provisioning, provider API calls, and use of repository secrets.

## Workflow topology

`go`, `web`, and `deployment` run independently for `push`, `pull_request`, and manual dispatch. `images` depends on all three, so a container image is never built from an unverified source revision. `release-readiness` depends on `images` and runs only for `workflow_dispatch`; it declares `environment: production`, allowing GitHub Environment required reviewers to pause the run before an operator treats the revision as deployable.

The release-readiness job only writes a GitHub job summary containing the commit SHA and image-build status. It does not log in to a registry, upload an image, or contact a deployment target.

## Network and secret boundaries

GitHub-hosted Linux runners use the official Go proxy by default and cache Go modules through `actions/setup-go`. Docker image builds receive `GOPROXY` as a non-secret build argument, defaulting to `https://proxy.golang.org,direct` in CI; the Compose production default remains configurable for domestic installations.

The deployment job supplies fixed validation-only values for RBAC bootstrap, Runner token, and TLS paths. It never reads `deploy/.env`, GitHub Secrets, or provider credentials. `validate-compose.ps1` runs `docker compose config --quiet`, so interpolated values are not written to the log.

## Failure behavior

Any failed test, Compose validation, or image build fails the workflow and prevents `images` and release readiness from running. A denied or unapproved GitHub Environment gate leaves the manual release run waiting or cancelled; it cannot cause a deployment. Docker build logs may be retained by GitHub Actions but must not contain user-provided `.env` files because `.dockerignore` excludes them.

## Acceptance criteria

1. A PR that breaks a Go test, Vitest test, deployment test, Compose definition, or Dockerfile fails in CI.
2. Every verified revision successfully builds Council, Runner, and Web images without a registry push.
3. Manual dispatch reaches a `production` Environment approval gate only after all verification and image builds succeed.
4. CI configuration contains no provider key, TLS private key, database password, Runner token, or registry credential.
5. Local Windows deployment behavior remains unchanged: the validation script still finds either `docker compose` or Docker Desktop's bundled `docker-compose.exe`.
