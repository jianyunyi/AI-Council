[CmdletBinding()]
param(
    [string]$EnvFile = (Join-Path $PSScriptRoot '.env'),
    [string]$BackupDirectory = (Join-Path (Split-Path $PSScriptRoot -Parent) '.data/backups'),
    [string]$ComposeFile = (Join-Path $PSScriptRoot 'docker-compose.yml')
)
$ErrorActionPreference = 'Stop'
if (-not (Test-Path -LiteralPath $EnvFile -PathType Leaf)) { throw "Environment file not found: $EnvFile" }
$backupKey = $null
Get-Content -LiteralPath $EnvFile | ForEach-Object {
    if ($_ -match '^\s*([^#\s][^=]*)=(.*)$' -and $Matches[1].Trim() -eq 'AICOUNCIL_BACKUP_KEY') { $backupKey = $Matches[2].Trim() }
}
if ($backupKey -notmatch '^[0-9a-fA-F]{64}$') { throw 'AICOUNCIL_BACKUP_KEY must be a 64-character hex key in deploy.env' }
if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { throw 'Docker CLI is required to back up the running Compose volume' }
New-Item -ItemType Directory -Force -Path $BackupDirectory | Out-Null
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$name = "aicouncil-$stamp.aicb"
$containerArchive = "/app/.data/backups/$name"
$hostArchive = Join-Path $BackupDirectory $name
$composeArgs = @('--env-file', $EnvFile, '-f', $ComposeFile)
# Pull the Runner database into the council volume so a single archive captures
# idempotency and workspace-registry state alongside council.db.
$runnerCopy = "/app/.data/backups/runner-$stamp.db"
& docker compose @composeArgs cp "runner:/app/.data/runner.db" "council:$runnerCopy"
if ($LASTEXITCODE -ne 0) { throw "Copying runner database into council volume failed with exit code $LASTEXITCODE" }
& docker compose @composeArgs exec -T council /aicouncil-admin backup --db /app/.data/council.db --runner-db $runnerCopy --artifacts /app/.data/artifacts --out $containerArchive
if ($LASTEXITCODE -ne 0) { throw "Backup creation in council volume failed with exit code $LASTEXITCODE" }
& docker compose @composeArgs exec -T council rm -f $runnerCopy
& docker compose @composeArgs cp "council:$containerArchive" $hostArchive
if ($LASTEXITCODE -ne 0) { throw "Copying backup archive from council volume failed with exit code $LASTEXITCODE" }
Write-Output "Encrypted backup created from Compose volume: $hostArchive"
