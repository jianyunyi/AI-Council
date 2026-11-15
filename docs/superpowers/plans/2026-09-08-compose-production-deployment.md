# Compose Production Deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a single Docker Compose deployment for HTTPS Caddy, Web, Council, and Runner with private service networking and persistent data.

**Architecture:** Caddy is the only published service and proxies the web console and Council routes on one internal network. Council, Runner, and Web run behind it with named volumes for state. Non-secret defaults live in an example environment file; runtime secrets are operator-provided.

**Tech Stack:** Docker Compose, Caddy, Next.js, Go services, SQLite volumes.

---

### Task 1: Add a production Web image

**Files:**
- Create: `deploy/Dockerfile.web`
- Modify: `web/package.json`

- [ ] **Step 1: Write a failing image build check**

Run: `docker build -f deploy/Dockerfile.web -t ai-council-web:test .`

Expected: FAIL because the Web Dockerfile does not yet exist.

- [ ] **Step 2: Add a multi-stage Dockerfile**

Create `deploy/Dockerfile.web` that installs with `corepack pnpm`, copies `web/package.json` and the lockfile before source, runs `pnpm --dir web build`, then starts `pnpm --dir web exec next start --hostname 0.0.0.0 --port 3000`. Set `NODE_ENV=production` in the runtime stage and expose port 3000.

```dockerfile
FROM node:22-alpine AS build
WORKDIR /src
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./web/
RUN pnpm --dir web install --frozen-lockfile
COPY web ./web
RUN pnpm --dir web build
```

- [ ] **Step 3: Verify the image build**

Run: `docker build -f deploy/Dockerfile.web -t ai-council-web:test .`

Expected: exit code 0.

### Task 2: Wire private Compose services and TLS ingress

**Files:**
- Modify: `deploy/docker-compose.yml`
- Modify: `deploy/Caddyfile`
- Create: `deploy/.env.example`

- [ ] **Step 1: Write the failing Compose validation command**

Run: `docker compose --env-file deploy/.env.example -f deploy/docker-compose.yml config`

Expected: FAIL because the current Compose file lacks `web`, `caddy`, certificate configuration, and RBAC bootstrap inputs.

- [ ] **Step 2: Add the `web` and `caddy` services**

Configure `web` with `deploy/Dockerfile.web` and no `ports`. Configure `caddy` with `caddy:2-alpine`, publish `80:80` and `443:443`, mount `deploy/Caddyfile` read-only plus named `caddy-data` and `caddy-config` volumes, and make it depend on `web` and `council`.

```yaml
caddy:
  image: caddy:2-alpine
  ports: ["80:80", "443:443"]
  volumes:
    - ./Caddyfile:/etc/caddy/Caddyfile:ro
    - caddy-data:/data
    - caddy-config:/config
```

- [ ] **Step 3: Make internal services private and persistent**

Remove every `ports` mapping from `council` and `runner`. Keep their database, artifact, and workspace volumes. Add an `app` network with `internal: true`, attach all services to it, and keep only Caddy attached to an additional public network so it can acquire or serve certificates.

- [ ] **Step 4: Configure Council RBAC and Runner connectivity**

Pass `--rbac`, `--rbac-bootstrap-subject`, and the Runner address required by the existing Council command to the Council service. Populate values using required `${COUNCIL_BOOTSTRAP_SUBJECT:?…}` and `${COUNCIL_BOOTSTRAP_PASSWORD:?…}` environment substitutions. Keep Provider API keys optional and never place literal values in Compose.

- [ ] **Step 5: Define non-secret example environment values**

Create `deploy/.env.example` with `COUNCIL_BOOTSTRAP_SUBJECT=admin`, `COUNCIL_UPSTREAM=council:8080`, `WEB_UPSTREAM=web:3000`, and placeholder comments for `COUNCIL_BOOTSTRAP_PASSWORD`, Provider keys, `TLS_CERT`, and `TLS_KEY`. Do not include a real secret.

- [ ] **Step 6: Verify Compose topology**

Run: `docker compose --env-file deploy/.env.example -f deploy/docker-compose.yml config`

Expected: rendered configuration includes four services, publishes ports only for Caddy, and resolves all named volumes and networks.

### Task 3: Document the operator flow

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Document required secrets and startup**

Add a production Compose section that instructs operators to copy `deploy/.env.example` to a non-committed `.env`, set bootstrap password, Provider keys, and TLS certificate paths, then run:

```powershell
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d --build
docker compose --env-file deploy/.env -f deploy/docker-compose.yml ps
```

- [ ] **Step 2: Document validation and shutdown**

Add commands for `/healthz`, initial `/login`, `docker compose logs`, and `docker compose down`. State that `down -v` removes persistent Council, Runner, workspace, and Caddy certificate data.

- [ ] **Step 3: Verify documentation and diff**

Run: `rg -n "docker compose|COUNCIL_BOOTSTRAP_PASSWORD|down -v" README.md` followed by `git diff --check`.

Expected: exit code 0.

### Task 4: Production configuration verification

**Files:**
- Modify: none

- [ ] **Step 1: Validate Compose**

Run: `docker compose --env-file deploy/.env.example -f deploy/docker-compose.yml config`

Expected: exit code 0.

- [ ] **Step 2: Build service images when Docker is available**

Run: `docker compose --env-file deploy/.env.example -f deploy/docker-compose.yml build`

Expected: exit code 0. If Docker Engine is unavailable on this Windows host, report the exact daemon error without altering Dockerfiles or Compose configuration.

- [ ] **Step 3: Run repository verification**

Run: `go test ./...` and `pnpm --dir web test`.

Expected: both exit code 0 after dependencies are installed through the configured proxy.
