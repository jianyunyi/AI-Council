# Compose Production Deployment Design

## Goal

Provide one Docker Compose deployment that serves the AI Council web console
and API through HTTPS while keeping Council and Runner off the public network.

## Services

- `caddy` is the sole public entry point. It publishes ports 80 and 443,
  terminates TLS, serves the Next.js console, and proxies `/api/*`,
  `/healthz`, and `/metrics` to Council.
- `web` runs the production Next.js server on the internal Compose network.
- `council` runs the Council API with RBAC enabled. Its SQLite database and
  artifact directory use named volumes. Bootstrap credentials arrive through
  environment variables and are never written into Compose source.
- `runner` runs the Workspace Runner on the internal network. Its workspace is
  a dedicated named volume; it has no published host port.

## Networking and Data

Compose creates one internal application network. Only Caddy has published
ports. Council, Web, and Runner resolve each other by service name. Named
volumes persist Council data, artifacts, and Runner workspace across container
restarts. TLS certificates use a Caddy data volume.

## Configuration

An `.env.example` documents non-secret host names, image tags, and required
bootstrap-subject variables. Production operators provide the bootstrap
password and Provider API keys through a non-committed `.env` file or their
secret manager. The Compose file must not contain real credentials or fallback
insecure defaults.

## Verification

The repository will provide a Compose configuration validation command and
document startup, initial login, health-check, and shutdown commands. Browser
E2E remains a CI/Linux concern; deployment verification proves service wiring
and readiness endpoints without requiring a local Chromium process.
