# Single-host backup and restore

## Host setup and lifecycle

Use Docker Desktop with Linux containers on Windows, or Docker Engine plus the Compose v2 plugin on Linux. DNS for the chosen public hostname must resolve to the host before starting Caddy; ports 80 and 443 must reach it. Permit inbound TCP 80/443 only. Do not publish Council, Runner, Prometheus, or Grafana ports. Compose keeps monitoring on the private app network.

Create a private `deploy/.env` from `deploy/.env.example`; do not check it in or paste its contents into logs. Generate independent random bootstrap password, Runner token, and 64-hex backup key. Set `PUBLIC_BASE_URL` to the exact HTTPS origin. Set SMTP host, port, and sender (and paired username/password when required) to enable email accounts. OAuth is optional; for each configured GitHub or Google provider, set both client ID and secret and register `https://your-host/api/v1/auth/oauth/<provider>/callback`. Put certificate and key files in the configured `TLS_CERT_DIRECTORY` (default `deploy/certs`) and set `TLS_CERT` / `TLS_KEY` to their `/certs/...` paths inside Caddy.

Preflight and deploy from the repository root with PowerShell 7 on either host OS:

```powershell
pwsh -File deploy/preflight.ps1
pwsh -File deploy/deploy.ps1
```

The deploy command tags application images with the current Git SHA by default. An explicit immutable tag can be passed with `-ReleaseTag`. It backs up a running prior Council deployment, pulls only Caddy and Prometheus, builds app images, activates Compose, checks Runner, Council, and Web container health plus public HTTPS `/healthz` and `/` routes, and records the release in `.data/deployments`. Failed first installs stop containers while preserving named volumes. Failed upgrades attempt to restore the prior recorded tag and verify health before reporting rollback success.

To roll back, use the recorded release tag after confirming all three local app images are available: `pwsh -File deploy/rollback.ps1 -ReleaseTag <tag>`. The command verifies Runner, Council, and Web container health plus the public HTTPS health and home routes before updating the active manifest. To stop services without deleting data, run `docker compose --env-file deploy/.env -f deploy/docker-compose.yml down`; do not add `-v`. Windows Docker Desktop bind mount paths must be shared with Docker; on Linux ensure the deployment user can access certificate, workspace, and backup directories. Keep firewall rules and DNS aligned when moving hosts.

## Runner workspaces and sharing

Compose mounts the host directory selected by `WORKSPACE_DIRECTORY` into Runner at `/workspace`. The default is `../workspaces` relative to the Compose project directory. Create the directory and place or clone repositories inside it before registering them in the web UI. For example, if the host directory is `F:\AI-council\workspaces`, clone a repository into `F:\AI-council\workspaces\team-a\service` and register `team-a/service` in the UI. The UI accepts Runner-relative paths only; absolute paths and paths outside the configured root are rejected.

On an existing deployment that used a Docker named volume for workspace data, set `WORKSPACE_DIRECTORY` to a host directory, stop the Compose stack, and copy the old volume contents into that directory before recreating Runner. Keep the old named volume until the new bind mount has been verified. Existing Council workspace rows are re-registered at startup only when their stored roots are safely inside the configured Runner root; unsafe legacy paths stop Council startup and require an operator to move the repository under the configured root and update the stored path before restarting.

Workspace owners can grant active members `viewer` or `editor` access. Viewers can read tasks, artifacts, and events. Editors can also create tasks and perform task actions, which still require the normal human approval before execution. Shares do not transfer Provider Keys or task ownership. Revocation blocks subsequent requests and closes an active event stream on its next authorization check; it does not stop a task that is already executing.

Runner keeps workspace ID-to-root registrations in its persistent SQLite database. Council registers a workspace through the authenticated internal gRPC connection before saving it. The Runner service remains on the internal Compose network and rejects every unregistered workspace ID.

The admin CLI creates one authenticated AES-256-GCM archive containing a consistent SQLite snapshot, every regular artifact file, and a SHA-256 manifest. It refuses to overwrite an archive or restore destination. Restores are extracted into a new isolated directory and are kept only after the database passes `PRAGMA integrity_check` and every manifest digest matches.

## Configure the archive key

Generate a random 32-byte key once, for example with `openssl rand -hex 32`, and store it in `deploy/.env` as `AICOUNCIL_BACKUP_KEY`. Compose requires it because the admin CLI runs inside the Council service. Keep a separate offline copy in an approved secret manager. Do not store the only key beside the backup archives: losing it makes all archives unrecoverable. Changing the key does not re-encrypt old archives; retain the old key for as long as archives encrypted with it are retained. The CLI accepts the key via environment only and never prints it.

## Create a backup

From the repository root on the host (or a mounted application checkout):

```powershell
powershell -ExecutionPolicy Bypass -File deploy/backup.ps1
```

The script asks the running Council container to snapshot `/app/.data/council.db` and `/app/.data/artifacts` from the actual `council-data` named volume, then copies the encrypted archive to a timestamped `.aicb` file under host `.data/backups`. The Council image includes the admin CLI, and Compose passes the backup key only through the service environment. Copy the result to protected off-host storage. Keep a bounded retention window according to the operator's recovery objectives; remove expired archives only after confirming a newer off-host copy exists.

For a standalone local data directory, the CLI can also be called directly:

```powershell
$env:AICOUNCIL_BACKUP_KEY = '<64 hex characters>'
go run ./cmd/aicouncil-admin backup --db .data/council.db --artifacts .data/artifacts --out .data/backups/manual.aicb
```

## Restore drill

The verification script copies the archive into the Council data volume and restores it to a timestamped, isolated `restore-drills` directory. It never writes over the live database or artifact directory:

```powershell
powershell -ExecutionPolicy Bypass -File deploy/restore-verify.ps1 -Archive .data/backups/aicouncil-YYYYMMDD-HHMMSS.aicb
```

After it succeeds, inspect the restored database and artifacts from the `council-data` Docker volume. To recover production, stop the services, preserve the current volume for investigation, then follow a separately reviewed cutover procedure using the verified restore. The verification script deliberately does not replace live data or start services. The drill directory and copied input archive consume space in the named volume and should be removed only after inspection and retention review.

An archive encrypted with a wrong key, modified archive bytes, missing manifest entry, hash mismatch, unsafe path, corrupt database, or pre-existing destination is rejected. Archive payloads are decrypted in memory, so provision enough memory and temporary disk for the compressed archive and restored data sizes.

## Account email

Email registration, verification, and password reset are enabled only when Compose has `SMTP_HOST`, `SMTP_PORT`, and `SMTP_FROM`. The mailer requires SMTP STARTTLS; use the submission port offered by your mail provider (commonly 587). Configure `SMTP_USERNAME` and `SMTP_PASSWORD` together when authentication is required. Keep credentials in the private `deploy/.env`, never in the example file. If SMTP is unset, registration and password reset return unavailable and bootstrap administrator login continues to work.

New email accounts cannot log in before verification. Reset tokens expire after one hour, verification tokens after 24 hours; both are one-time and stored in SQLite only as SHA-256 hashes. Password reset revokes all existing sessions for that account. Existing resources without an owner are assigned to the configured bootstrap administrator during startup. Council will refuse startup if such resources exist but no bootstrap subject is available, so establish the administrator before upgrading.

## Per-user model credentials and OAuth

To enable the browser Provider page, set `PROVIDER_KEY_ENCRYPTION_KEY` in the private `deploy/.env` to an independently generated 32-byte key encoded as 64 hex characters (for example, `openssl rand -hex 32`). Keep this value stable and store an offline copy: provider keys in SQLite are encrypted with AES-256-GCM and bound to their owner and profile metadata, so changing or losing the master key makes them unreadable. User-supplied provider keys are used for that user's Council tasks; if the account has no saved keys, the server falls back to configured deployment provider keys. Provider management requires RBAC to be enabled.

GitHub and Google sign-in are optional. Configure `PUBLIC_BASE_URL` as the public HTTPS origin, then set a complete client ID and secret pair for at least one provider. Register its callback as `{PUBLIC_BASE_URL}/api/v1/auth/oauth/{github|google}/callback`. OAuth flow state, PKCE verifier, and OIDC nonce are single-use and expire after ten minutes. The OAuth client secrets and Provider Key encryption key belong in private deployment secrets, not this file or source control.
