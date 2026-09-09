# Compose production deployment

This deployment places Caddy at the public edge, the Next.js console and Council
behind it, and the Workspace Runner on the private application network. Caddy
is the only service that publishes host ports (`80` and `443`). Council has a
separate egress network solely so that it can call configured model providers;
it does not publish a Council port.

## Prerequisites

- Docker Engine with the Docker Compose v2 plugin.
- A DNS name that resolves to this host, plus a TLS certificate and private key
  for that name. Caddy uses supplied certificates; this Compose file does not
  obtain certificates automatically.
- Strong, unique values for the initial administrator password and Runner token.

Run all commands below from the repository root. Create the local environment
file, which is ignored by Git:

```powershell
Copy-Item deploy/.env.example deploy/.env
```

Set these required values in `deploy/.env` (or inject them from the deployment
platform's secret manager):

| Variable | Purpose |
| --- | --- |
| `COUNCIL_BOOTSTRAP_SUBJECT` | Initial RBAC administrator login name. The example uses `admin`. |
| `COUNCIL_BOOTSTRAP_PASSWORD` | Strong password for that administrator. |
| `RUNNER_TOKEN` | Long random shared secret used by Council to authenticate to the private Runner gRPC service. |
| `TLS_CERT` | Certificate path *inside the Caddy container*, normally `/certs/tls.crt`. |
| `TLS_KEY` | Private-key path *inside the Caddy container*, normally `/certs/tls.key`. |
| `TLS_CERT_DIRECTORY` | Host directory mounted read-only at `/certs`; by default `./certs`, relative to `deploy/`. |

For the default paths, place the certificate at `deploy/certs/tls.crt` and the
key at `deploy/certs/tls.key`, retain `TLS_CERT=/certs/tls.crt` and
`TLS_KEY=/certs/tls.key`, and set `TLS_CERT_DIRECTORY=./certs`. If the host
directory changes, the two TLS file variables still refer to the `/certs`
container mount, not host paths. Keep `deploy/.env`, certificates, private keys,
and all provider credentials out of source control.

`OPENAI_API_KEY`, `DEEPSEEK_API_KEY`, and `ANTHROPIC_API_KEY` are optional.
Their matching `*_MODEL` values are optional model overrides. Configure only
providers that the installation is allowed to use; Council needs its egress
network for these API calls.

## Validate and start

The validation helper supplies temporary non-secret placeholders only for
Compose interpolation and restores the calling shell environment afterwards:

```powershell
powershell -ExecutionPolicy Bypass -File deploy/validate-compose.ps1
```

It verifies `docker compose config`; it does not read certificate contents or
start containers. Then build and start the stack:

```powershell
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d --build
docker compose --env-file deploy/.env -f deploy/docker-compose.yml ps
docker compose --env-file deploy/.env -f deploy/docker-compose.yml logs --follow
```

The services restart with `unless-stopped`. Caddy waits for the web console and
Council health checks; Council waits for Runner health before it starts.

## First login and checks

After DNS and TLS are in place, open `https://your-domain/login` and sign in
using `COUNCIL_BOOTSTRAP_SUBJECT` and `COUNCIL_BOOTSTRAP_PASSWORD`. Confirm that
the administrator can reach `/admin/users`. Keep the required Compose variables
in the deployment secret store (not in Git), rotate them according to the
organization's secret policy, preserve ordinary administrator accounts, and
keep RBAC enabled.

Check public ingress with:

```powershell
curl.exe -f https://your-domain/healthz
docker compose --env-file deploy/.env -f deploy/docker-compose.yml ps
```

Only `/api/*`, `/healthz`, and the web application are routed by Caddy.
`/metrics` is deliberately not public. Scrape it only from a protected service
attached to the private `app` network, or add a separately authenticated and
network-restricted metrics route. Do not expose it through the public Caddy
site without access controls.

## Upgrade, stop, and data

To deploy a new checked-out version, validate, rebuild, and reconcile services:

```powershell
powershell -ExecutionPolicy Bypass -File deploy/validate-compose.ps1
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d --build
```

For a graceful stop that preserves persistent data, allow services up to 30
seconds to finish before removing containers:

```powershell
docker compose --env-file deploy/.env -f deploy/docker-compose.yml stop -t 30
docker compose --env-file deploy/.env -f deploy/docker-compose.yml down
```

`down` does not remove the named volumes. The stack persists Caddy state in
`caddy-data` and `caddy-config`, Council SQLite state and artifacts in
`council-data`, Runner idempotency state in `runner-data`, and the managed
workspace in `workspace-data`. Before host, Docker, or application upgrades,
stop the stack and take a restorable, access-controlled backup of every named
volume—especially `council-data` and `workspace-data`. List the resolved volume
names with `docker volume ls`; Compose prefixes names with its project name.
Test restoration on a separate host before relying on a backup. Do not run
`docker compose down --volumes` unless intentionally destroying all Council,
Runner, workspace, and Caddy data.
